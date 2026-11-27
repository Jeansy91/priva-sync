package handler

import "privasync/server/internal/errcode"

// Stable, language-agnostic error codes returned in JSON error responses
// alongside a human-readable English fallback. Web clients translate via
// the `code`; the `error` string is only displayed when no translation
// matches. SCREAMING_SNAKE_CASE; never repurpose an existing value.

const (
	// Generic
	ErrInternal      = "INTERNAL_ERROR"
	ErrInvalidBody   = "INVALID_REQUEST_BODY"
	ErrInvalidPath   = "INVALID_PATH"
	ErrUnsupportedOp = "UNSUPPORTED_OPERATION"
	ErrTooManyPaths  = "TOO_MANY_PATHS"

	// Auth / session
	ErrInvalidCredentials   = "INVALID_CREDENTIALS" //#nosec G101 -- stable error-code string, not a credential
	ErrTooManyAttempts      = "TOO_MANY_ATTEMPTS"
	ErrAccountDisabled      = "ACCOUNT_DISABLED"
	ErrMissingCredentials   = "MISSING_CREDENTIALS"
	ErrInvalidSessionToken  = "INVALID_SESSION_TOKEN"
	ErrInvalidRefreshToken  = "INVALID_REFRESH_TOKEN"
	ErrRefreshTokenExpired  = "REFRESH_TOKEN_EXPIRED" //#nosec G101 -- stable error-code string, not a credential
	ErrNoRefreshToken       = "NO_REFRESH_TOKEN"
	ErrInvalidSetupToken    = "INVALID_SETUP_TOKEN"
	ErrSetupAlreadyComplete = "SETUP_ALREADY_COMPLETE"

	// 2FA
	ErrTotpNotEnabled    = "TOTP_NOT_ENABLED"
	ErrTotpNotSetUp      = "TOTP_NOT_SET_UP"
	ErrInvalidTotpCode   = "INVALID_TOTP_CODE"
	ErrInvalidBackupCode = "INVALID_BACKUP_CODE"
	ErrTotpCodeRequired  = "TOTP_OR_BACKUP_REQUIRED"

	// App passwords (WebDAV credentials)
	ErrInvalidAppPasswordName = "APP_PASSWORD_NAME_INVALID" //#nosec G101 -- stable error-code string, not a credential
	ErrInvalidAppPasswordID   = "APP_PASSWORD_ID_INVALID"   //#nosec G101 -- stable error-code string, not a credential
	ErrAppPasswordNotFound    = "APP_PASSWORD_NOT_FOUND"    //#nosec G101 -- stable error-code string, not a credential

	// Passkey / WebAuthn
	ErrPasskeyChallengeInvalid   = "PASSKEY_CHALLENGE_INVALID"
	ErrPasskeyVerificationFailed = "PASSKEY_VERIFICATION_FAILED" //#nosec G101 -- stable error-code string, not a credential
	ErrPasskeyNotFound           = "PASSKEY_NOT_FOUND"           //#nosec G101 -- stable error-code string, not a credential

	// Password / token
	ErrCurrentPasswordWrong = "CURRENT_PASSWORD_WRONG"
	ErrPasswordTooShort     = "PASSWORD_TOO_SHORT"
	ErrPasswordTooShortNew  = "NEW_PASSWORD_TOO_SHORT"
	ErrPasswordResetMissing = "PASSWORD_RESET_FIELDS_REQUIRED"
	ErrTokenRequired        = "TOKEN_REQUIRED"
	ErrTokenInvalid         = "TOKEN_INVALID_OR_EXPIRED"
	ErrEmailInvalid         = "EMAIL_INVALID"
	ErrEmailTaken           = "EMAIL_TAKEN"

	// User / registration
	ErrUserNotFound       = "USER_NOT_FOUND"
	ErrTargetUserNotFound = "TARGET_USER_NOT_FOUND"
	ErrInvalidUserID      = "INVALID_USER_ID"
	ErrInvalidTargetUser  = "INVALID_TARGET_USER"
	ErrUsernameTaken      = "USERNAME_OR_EMAIL_TAKEN"
	ErrRegistrationFields = "REGISTRATION_FIELDS_REQUIRED"

	// Avatar
	ErrAvatarTooLarge   = "AVATAR_TOO_LARGE"
	ErrAvatarMissing    = "AVATAR_FIELD_MISSING"
	ErrAvatarInvalid    = "AVATAR_INVALID_IMAGE"
	ErrAvatarNotPresent = "AVATAR_NOT_PRESENT"

	// Files / paths
	ErrFileNotFound        = "FILE_NOT_FOUND"
	ErrSourceNotFound      = "SOURCE_NOT_FOUND"
	ErrSourceNotInDB       = "SOURCE_NOT_IN_DATABASE"
	ErrSourceMoved         = "SOURCE_FILE_MOVED"
	ErrInvalidFileID       = "INVALID_FILE_ID"
	ErrInvalidFilename     = "INVALID_FILENAME"
	ErrInvalidFromPath     = "INVALID_FROM_PATH"
	ErrInvalidToPath       = "INVALID_TO_PATH"
	ErrSamePath            = "FROM_TO_PATHS_IDENTICAL"
	ErrPathRequired        = "PATH_REQUIRED"
	ErrNameRequired        = "NAME_REQUIRED"
	ErrDestinationOccupied = "DESTINATION_PATH_OCCUPIED"
	ErrOriginalOccupied    = "ORIGINAL_PATH_OCCUPIED"
	ErrMoveFailed          = "MOVE_FAILED"
	ErrRestoreFailed       = "RESTORE_FAILED"

	// Trash
	ErrTrashedNotFound = "TRASHED_FILE_NOT_FOUND"
	ErrTrashedGone     = "TRASHED_FILE_GONE"

	// Conflicts
	ErrConflictNotFound = "CONFLICT_NOT_FOUND"
	ErrInvalidConflict  = "INVALID_CONFLICT_ID"
	ErrInvalidStrategy  = "INVALID_RESOLVE_STRATEGY"

	// Sync roots
	ErrInvalidRootID    = "INVALID_ROOT_ID"
	ErrSyncRootNotFound = "SYNC_ROOT_NOT_FOUND"
	ErrSyncRootExists   = "SYNC_ROOT_ALREADY_EXISTS"

	// Upload
	ErrInvalidUploadID     = "INVALID_UPLOAD_ID"
	ErrInvalidChunkOffset  = "INVALID_CHUNK_OFFSET"
	ErrChunkLengthRequired = "CHUNK_LENGTH_REQUIRED"
	ErrChunkOutOfRange     = "CHUNK_RANGE_OUT_OF_BOUNDS"
	ErrChunkTooLarge       = "CHUNK_TOO_LARGE"
	ErrUploadSessionGone   = "UPLOAD_SESSION_EXPIRED"
	ErrUploadSessionNF     = "UPLOAD_SESSION_NOT_FOUND"
	ErrUploadSessionMixed  = "UPLOAD_SESSION_NOT_FOUND_OR_EXPIRED"
	ErrUploadFields        = "UPLOAD_FIELDS_REQUIRED"
	ErrSessionWrongRoot    = "SESSION_WRONG_ROOT"
	ErrChunkStoreFailed    = "CHUNK_STORE_FAILED"
	ErrAssemblyFailed      = errcode.AssemblyFailed
	ErrUploadFailed        = errcode.UploadFailed
	ErrFinalizeFailed      = "FINALIZE_FAILED"
	ErrMultipartRequired   = "MULTIPART_REQUIRED"
	ErrMultipartMalformed  = "MULTIPART_MALFORMED"
	ErrContentHashMismatch = errcode.ContentHashMismatch
	ErrChunkMissing        = errcode.ChunksMissing
	// The session left the open state: a complete was accepted for it, or
	// it already has a verdict. Either way the chunks are no longer the
	// client's to change.
	ErrUploadSessionClosed = "UPLOAD_SESSION_CLOSED"
	ErrChunkLengthMismatch = "CHUNK_LENGTH_MISMATCH"
	ErrAssembledSizeWrong  = errcode.AssembledSizeWrong

	// Delta-Sync (ADR-0002)
	ErrDeltaNoBase    = "DELTA_NO_BASE"
	ErrDeltaInvalidOp = "DELTA_INVALID_OP"
	ErrDeltaFailed    = "DELTA_RECONSTRUCTION_FAILED"

	// Versions
	ErrInvalidVersion = "INVALID_VERSION"

	// Shares
	ErrInvalidShareID   = "INVALID_SHARE_ID"
	ErrShareNotFound    = "SHARE_NOT_FOUND"
	ErrShareExpired     = "SHARE_EXPIRED"
	ErrShareSelf        = "SHARE_SELF_FORBIDDEN"
	ErrShareTypeInvalid = "SHARE_TYPE_INVALID"
	ErrShareFieldsMiss  = "SHARE_FIELDS_REQUIRED"
	ErrShareUserMissing = "SHARE_TARGET_USER_REQUIRED"
	ErrInvalidExpiresAt = "INVALID_EXPIRES_AT"
	ErrInvalidSince     = "INVALID_SINCE_PARAM"
	ErrMountReadOnly    = "MOUNT_READ_ONLY"
	// ErrFeedVersionRequired: the changes feed now merges files rows with
	// move_events (ADR-0065) -- a client that never sends feed_version=2
	// would see a directory move as silently missing data instead of a loud
	// error.
	ErrFeedVersionRequired = "FEED_VERSION_REQUIRED"

	// Quota
	ErrQuotaExceeded = errcode.QuotaExceeded
	ErrDiskFull      = "DISK_FULL"

	// Misc
	ErrInvalidTarget = "INVALID_TARGET"

	// Clients (device overview)
	ErrInvalidClientID = "INVALID_CLIENT_ID"
	ErrClientNotFound  = "CLIENT_NOT_FOUND"

	// Office preview (ADR-0027)
	ErrDocumentServerDisabled = "DOCUMENT_SERVER_DISABLED"
	ErrUnsupportedFileType    = "UNSUPPORTED_FILE_TYPE"
)
