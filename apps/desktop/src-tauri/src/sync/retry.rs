//! Retry policy + transient-error classification used by the sync engine.
//!
//! Two distinct backoff schedules live here:
//!   * [`BACKOFF_SECS`] / [`with_retry`] / [`with_retry_throttled`] — in-cycle
//!     retries for a single network operation. Bounded length, exponential
//!     delays. The `_throttled` variant additionally gates each attempt on an
//!     upload-concurrency semaphore, releasing it during the backoff sleep so
//!     a spinning failure doesn't starve the rest of the upload queue.
//!   * [`FAILURE_BACKOFF_SECS`] / [`compute_next_retry`] — schedule for
//!     persistent `pending_failed` rows that are retried in later sync
//!     cycles. Returns `None` after the last bucket, meaning manual-only.

use std::time::Duration;
use anyhow::Result;
use tracing::{info, warn};

/// Delay schedule for transient errors: immediate, 5 s, 30 s, 2 min, 10 min.
/// Total attempts = length of this array.
pub(super) const BACKOFF_SECS: &[u64] = &[0, 5, 30, 120, 600];

/// Backoff schedule for failed-state retries: 1m, 5m, 30m, 2h, 12h, then NULL
/// = manual-only. Index = attempt_count after the failure (1-based).
const FAILURE_BACKOFF_SECS: &[i64] = &[60, 5 * 60, 30 * 60, 2 * 3600, 12 * 3600];

/// Fixed retry interval for mount uploads blocked by the giver's full quota
/// (ADR-0011). Unlike `pending_failed` this does NOT back off — the wait is on
/// the giver freeing space, which can happen any time, so a steady short poll
/// is the right cadence. The desktop does not subscribe to the giver's quota
/// SSE (that would leak the giver's storage level), hence the poll.
pub(super) const REMOTE_QUOTA_RETRY_SECS: i64 = 60;

/// Fixed retry interval for uploads deferred because another program has the
/// file open (ADR-0077). Same reasoning as the remote-quota wait: the user can
/// close it any time, and the lock file's removal is excluded from the watcher.
pub(super) const IN_USE_RETRY_SECS: i64 = 60;

/// Compute the next retry timestamp (unix-seconds) for a row that just failed
/// with attempt_count `n`. Returns `None` after the last bucket — the row
/// stays in pending_failed / failed_server_changes until the user clicks Retry.
pub(super) fn compute_next_retry(now: i64, attempt_count: i64) -> Option<i64> {
    let idx = (attempt_count - 1).max(0) as usize;
    FAILURE_BACKOFF_SECS.get(idx).map(|sec| now + sec)
}

/// Returns `true` for errors that make sense to retry (network, 5xx, timeout).
/// HTTP 4xx (except 429) are permanent — retrying won't help.
pub(super) fn is_transient_error(e: &anyhow::Error) -> bool {
    let s = e.to_string();
    // `check_status` in api.rs formats permanent errors as "…: HTTP 4xx …".
    // 507 (Insufficient Storage) is not retried in-cycle either: a full quota
    // won't clear within the in-cycle backoff window, so we let it fall
    // straight through to the failure/quota-block handler (see ADR-0011).
    for code in &["HTTP 400", "HTTP 401", "HTTP 403", "HTTP 404",
                  "HTTP 409", "HTTP 410", "HTTP 413", "HTTP 422", "HTTP 507"] {
        if s.contains(code) { return false; }
    }
    true
}

/// True for the giver-quota-full response on a mount upload (HTTP 507). Drives
/// the `blocked_remote_quota` status with its own fixed retry cadence instead
/// of the exponential `pending_failed` backoff (ADR-0011).
pub(super) fn is_remote_quota_error(e: &anyhow::Error) -> bool {
    e.to_string().contains("HTTP 507")
}

/// Retry `f` up to `BACKOFF_SECS.len()` times with exponential back-off.
/// Only transient errors trigger a retry; permanent errors propagate immediately.
pub(super) async fn with_retry<F, Fut, T>(label: &str, mut f: F) -> Result<T>
where
    F: FnMut() -> Fut,
    Fut: std::future::Future<Output = Result<T>>,
{
    for (attempt, &delay) in BACKOFF_SECS.iter().enumerate() {
        if delay > 0 {
            info!("retry {}/{} '{}' in {}s",
                attempt, BACKOFF_SECS.len() - 1, label, delay);
            tokio::time::sleep(Duration::from_secs(delay)).await;
        }
        match f().await {
            Ok(v) => return Ok(v),
            Err(e) if attempt + 1 < BACKOFF_SECS.len() && is_transient_error(&e) => {
                warn!("transient error '{}' (attempt {}/{}): {e}",
                    label, attempt + 1, BACKOFF_SECS.len());
            }
            Err(e) => return Err(e),
        }
    }
    unreachable!()
}

/// Same retry contract as [`with_retry`], but each attempt is additionally
/// gated on `sem` — an upload-concurrency slot is held only for the duration
/// of the attempt itself, not across the backoff sleep between attempts. The
/// slot is a local binding scoped to the attempt block, so it drops (and
/// frees the slot) before the next `sleep`. Without this, a handful of
/// persistently-failing large uploads can occupy every concurrency slot for
/// the whole ~13-minute backoff window (see [`BACKOFF_SECS`]) and starve an
/// otherwise-healthy queue of thousands of unrelated files.
pub(super) async fn with_retry_throttled<F, Fut, T>(
    sem: &tokio::sync::Semaphore, label: &str, mut f: F,
) -> Result<T>
where
    F: FnMut() -> Fut,
    Fut: std::future::Future<Output = Result<T>>,
{
    for (attempt, &delay) in BACKOFF_SECS.iter().enumerate() {
        if delay > 0 {
            info!("retry {}/{} '{}' in {}s (upload slot released while waiting)",
                attempt, BACKOFF_SECS.len() - 1, label, delay);
            tokio::time::sleep(Duration::from_secs(delay)).await;
        }
        let result = {
            let _permit = sem.acquire().await.expect("semaphore closed");
            f().await
        };
        match result {
            Ok(v) => return Ok(v),
            Err(e) if attempt + 1 < BACKOFF_SECS.len() && is_transient_error(&e) => {
                warn!("transient error '{}' (attempt {}/{}): {e}",
                    label, attempt + 1, BACKOFF_SECS.len());
            }
            Err(e) => return Err(e),
        }
    }
    unreachable!()
}

#[cfg(test)]
mod tests {
    use super::*;

    // 507 (giver quota full on a mount upload) must be classified as a remote-
    // quota block, and must NOT be retried in-cycle — otherwise the in-cycle
    // backoff would spin on a wait that only the giver can clear. ADR-0011.
    #[test]
    fn remote_quota_error_is_detected_and_not_in_cycle_retried() {
        let e = anyhow::anyhow!("upload file: HTTP 507 Insufficient Storage");
        assert!(is_remote_quota_error(&e));
        assert!(!is_transient_error(&e));
    }

    // A plain network error is transient (in-cycle retry) and not a quota block.
    #[test]
    fn network_error_is_transient_not_quota() {
        let e = anyhow::anyhow!("connection reset by peer");
        assert!(!is_remote_quota_error(&e));
        assert!(is_transient_error(&e));
    }

    // The 4xx codes (except 429, which is not in the list) are permanent —
    // retrying an in-cycle op would just waste the backoff window.
    #[test]
    fn permanent_http_errors_are_not_transient() {
        for code in ["HTTP 400", "HTTP 401", "HTTP 403", "HTTP 404",
                     "HTTP 409", "HTTP 410", "HTTP 413", "HTTP 422"] {
            let e = anyhow::anyhow!(format!("upload file: {code} bad request"));
            assert!(!is_transient_error(&e), "{code} must not be transient");
        }
    }

    // compute_next_retry walks the fixed backoff schedule (1m, 5m, 30m, 2h,
    // 12h) keyed by attempt_count, then returns None — the row falls back to
    // manual-only retry once every bucket is exhausted.
    #[test]
    fn compute_next_retry_walks_backoff_schedule_then_manual_only() {
        let now = 1_000_000i64;
        assert_eq!(compute_next_retry(now, 1), Some(now + 60));
        assert_eq!(compute_next_retry(now, 2), Some(now + 5 * 60));
        assert_eq!(compute_next_retry(now, 3), Some(now + 30 * 60));
        assert_eq!(compute_next_retry(now, 4), Some(now + 2 * 3600));
        assert_eq!(compute_next_retry(now, 5), Some(now + 12 * 3600));
        assert_eq!(compute_next_retry(now, 6), None, "exhausted schedule must fall back to manual-only");
        assert_eq!(compute_next_retry(now, 100), None);
    }

    // attempt_count <= 0 is defensive: clamp to the first bucket instead of
    // panicking on the `attempt_count - 1` index arithmetic.
    #[test]
    fn compute_next_retry_clamps_non_positive_attempt_count() {
        let now = 500i64;
        assert_eq!(compute_next_retry(now, 0), Some(now + 60));
        assert_eq!(compute_next_retry(now, -5), Some(now + 60));
    }

    // Success on the very first attempt hits the delay=0 branch of
    // BACKOFF_SECS, so this returns without ever calling tokio::time::sleep --
    // safe to run without pausing/advancing the clock.
    #[tokio::test]
    async fn with_retry_returns_immediately_on_first_success() {
        let calls = std::sync::atomic::AtomicU32::new(0);
        let result = with_retry("op", || {
            calls.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            async { Ok::<_, anyhow::Error>(42) }
        }).await;

        assert_eq!(result.unwrap(), 42);
        assert_eq!(calls.load(std::sync::atomic::Ordering::SeqCst), 1, "must not retry after a first-try success");
    }

    // A permanent error (not transient) must propagate on the very first
    // attempt without looping into a sleeping retry.
    #[tokio::test]
    async fn with_retry_propagates_permanent_error_without_retrying() {
        let calls = std::sync::atomic::AtomicU32::new(0);
        let result = with_retry("op", || {
            calls.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            async { Err::<i32, _>(anyhow::anyhow!("upload file: HTTP 404 not found")) }
        }).await;

        assert!(result.is_err());
        assert_eq!(calls.load(std::sync::atomic::Ordering::SeqCst), 1, "a permanent error must not trigger a retry");
    }

    // The whole point of with_retry_throttled: the concurrency slot must be
    // free again *while* a retry is backing off, not just after the retry
    // loop finishes — otherwise a spinning failure starves the rest of an
    // upload queue that shares the same semaphore.
    #[tokio::test(start_paused = true)]
    async fn with_retry_throttled_releases_slot_during_backoff_sleep() {
        let sem = tokio::sync::Semaphore::new(1);
        let attempts = std::sync::atomic::AtomicU32::new(0);

        let task = with_retry_throttled(&sem, "op", || {
            let n = attempts.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            async move {
                if n == 0 {
                    Err::<i32, _>(anyhow::anyhow!("connection reset by peer"))
                } else {
                    Ok(42)
                }
            }
        });
        tokio::pin!(task);

        // Drive the future to the point where the first (failing) attempt has
        // returned and it is parked in the 5s backoff sleep before the next
        // attempt — the shortest concurrent timer wins the race under a
        // paused, auto-advancing clock.
        tokio::select! {
            _ = &mut task => panic!("must not resolve before the retry succeeds"),
            _ = tokio::time::sleep(Duration::from_millis(1)) => {}
        }
        assert_eq!(sem.available_permits(), 1, "slot must be released while sleeping between attempts");

        assert_eq!(task.await.unwrap(), 42);
    }
}
