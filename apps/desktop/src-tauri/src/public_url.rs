//! Normalisation of the public PrivaSync address the user configures.
//!
//! The desktop is pointed at the **public web URL** (e.g. `https://sync.host`);
//! the API is reached under the same origin (`/api/v1`, via reverse proxy), and
//! the Explorer context-menu deep links use it too (ADR-0021). Users paste this
//! URL in many shapes — with/without scheme, trailing slash, or even the
//! `/api` path — so we normalise to a bare origin before storing it.

/// Normalise a user-entered address to a bare origin (scheme + host[:port]),
/// without trailing slash or an accidental `/api[/v1]` suffix. Returns an empty
/// string for blank input so the caller can reject it.
pub fn normalize_public_url(input: &str) -> String {
    let s = input.trim();
    if s.is_empty() {
        return String::new();
    }

    // Ensure a scheme. Loopback hosts default to http (dev), everything else to
    // https (the public deployment is TLS-terminated).
    let with_scheme = if s.contains("://") {
        s.to_string()
    } else {
        let host = s.split('/').next().unwrap_or(s);
        let local = host.starts_with("localhost") || host.starts_with("127.0.0.1");
        format!("{}://{}", if local { "http" } else { "https" }, s)
    };

    let mut out = with_scheme.trim_end_matches('/').to_string();

    // Strip an accidentally-pasted API path so the stored value is the web base.
    for suffix in ["/api/v1", "/api"] {
        if out.to_lowercase().ends_with(suffix) {
            out.truncate(out.len() - suffix.len());
            out = out.trim_end_matches('/').to_string();
            break;
        }
    }
    out
}

/// True when `url` would send credentials in clear text over a network we
/// cannot assume to be private: plain `http://` to a host that is neither
/// loopback nor a private/link-local address nor a single-label or `.local`/
/// `.lan` name. `https://` and local development/LAN setups are fine.
pub fn is_cleartext_remote(url: &str) -> bool {
    let Ok(parsed) = reqwest::Url::parse(url) else { return false };
    if parsed.scheme() != "http" {
        return false;
    }
    match parsed.host() {
        None => false,
        Some(url::Host::Ipv4(ip)) => !(ip.is_loopback() || ip.is_private() || ip.is_link_local()),
        Some(url::Host::Ipv6(ip)) => {
            let seg0 = ip.segments()[0];
            let unique_local = (seg0 & 0xfe00) == 0xfc00;
            let link_local = (seg0 & 0xffc0) == 0xfe80;
            !(ip.is_loopback() || unique_local || link_local)
        }
        Some(url::Host::Domain(name)) => {
            let n = name.trim_end_matches('.').to_ascii_lowercase();
            let local = n == "localhost"
                || n.ends_with(".localhost")
                || !n.contains('.')
                || n.ends_with(".local")
                || n.ends_with(".lan");
            !local
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn strips_trailing_slash() {
        assert_eq!(normalize_public_url("https://sync.example.com/"), "https://sync.example.com");
    }

    #[test]
    fn adds_https_scheme_when_missing() {
        assert_eq!(normalize_public_url("sync.example.com"), "https://sync.example.com");
    }

    #[test]
    fn strips_api_v1_suffix() {
        assert_eq!(normalize_public_url("https://sync.example.com/api/v1"), "https://sync.example.com");
    }

    #[test]
    fn strips_bare_api_suffix() {
        assert_eq!(normalize_public_url("https://sync.example.com/api/"), "https://sync.example.com");
    }

    #[test]
    fn keeps_explicit_http_for_localhost() {
        assert_eq!(normalize_public_url("http://localhost:1420"), "http://localhost:1420");
    }

    #[test]
    fn defaults_localhost_to_http() {
        assert_eq!(normalize_public_url("localhost:8080"), "http://localhost:8080");
        assert_eq!(normalize_public_url("127.0.0.1:8080"), "http://127.0.0.1:8080");
    }

    #[test]
    fn trims_whitespace_and_redundant_slashes() {
        assert_eq!(normalize_public_url("  https://x.example//  "), "https://x.example");
    }

    #[test]
    fn blank_stays_blank() {
        assert_eq!(normalize_public_url("   "), "");
    }

    #[test]
    fn cleartext_is_flagged_only_for_remote_http() {
        assert!(is_cleartext_remote("http://sync.example.com"));
        assert!(is_cleartext_remote("http://8.8.8.8:8080"));
        assert!(!is_cleartext_remote("https://sync.example.com"));
        assert!(!is_cleartext_remote("http://localhost:8080"));
        assert!(!is_cleartext_remote("http://127.0.0.1:8080"));
        assert!(!is_cleartext_remote("http://[::1]:8080"));
        assert!(!is_cleartext_remote("http://192.168.1.20:8080"));
        assert!(!is_cleartext_remote("http://nas:8080"));
        assert!(!is_cleartext_remote("http://nas.local"));
        assert!(!is_cleartext_remote("not a url"));
    }
}
