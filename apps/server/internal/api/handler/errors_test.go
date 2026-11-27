package handler

import "testing"

// All exported error codes must be unique — collisions silently mistranslate
// on the client.
func TestErrorCodesUnique(t *testing.T) {
	codes := []string{
		ErrInternal, ErrInvalidBody, ErrInvalidPath, ErrUnsupportedOp,
		ErrInvalidCredentials, ErrTooManyAttempts, ErrAccountDisabled, ErrMissingCredentials,
		ErrInvalidSessionToken, ErrInvalidRefreshToken, ErrRefreshTokenExpired, ErrNoRefreshToken,
		ErrInvalidSetupToken, ErrSetupAlreadyComplete,
		ErrTotpNotEnabled, ErrTotpNotSetUp, ErrInvalidTotpCode, ErrInvalidBackupCode, ErrTotpCodeRequired,
		ErrCurrentPasswordWrong, ErrPasswordTooShort, ErrPasswordTooShortNew, ErrPasswordResetMissing,
		ErrTokenRequired, ErrTokenInvalid, ErrEmailInvalid, ErrEmailTaken,
		ErrUserNotFound, ErrTargetUserNotFound, ErrInvalidUserID, ErrInvalidTargetUser, ErrUsernameTaken, ErrRegistrationFields,
		ErrAvatarTooLarge, ErrAvatarMissing, ErrAvatarInvalid, ErrAvatarNotPresent,
		ErrFileNotFound, ErrSourceNotFound, ErrSourceNotInDB, ErrSourceMoved,
		ErrInvalidFileID, ErrInvalidFilename, ErrInvalidFromPath, ErrInvalidToPath, ErrSamePath,
		ErrPathRequired, ErrNameRequired, ErrDestinationOccupied, ErrOriginalOccupied,
		ErrMoveFailed, ErrRestoreFailed,
		ErrTrashedNotFound, ErrTrashedGone,
		ErrConflictNotFound, ErrInvalidConflict, ErrInvalidStrategy,
		ErrInvalidRootID, ErrSyncRootNotFound, ErrSyncRootExists,
		ErrInvalidUploadID, ErrInvalidChunkOffset, ErrChunkLengthRequired, ErrChunkOutOfRange, ErrChunkTooLarge,
		ErrUploadSessionGone, ErrUploadSessionNF, ErrUploadSessionMixed,
		ErrUploadFields, ErrSessionWrongRoot, ErrChunkStoreFailed, ErrChunkLengthMismatch,
		ErrUploadSessionClosed, ErrAssembledSizeWrong,
		ErrAssemblyFailed, ErrUploadFailed, ErrFinalizeFailed,
		ErrMultipartRequired, ErrMultipartMalformed, ErrContentHashMismatch, ErrChunkMissing,
		ErrInvalidVersion,
		ErrInvalidShareID, ErrShareNotFound, ErrShareExpired, ErrShareSelf,
		ErrShareTypeInvalid, ErrShareFieldsMiss, ErrShareUserMissing,
		ErrInvalidExpiresAt, ErrInvalidSince,
		ErrQuotaExceeded,
		ErrInvalidTarget,
	}
	seen := make(map[string]int, len(codes))
	for i, c := range codes {
		if c == "" {
			t.Errorf("code at index %d is empty", i)
			continue
		}
		if prev, ok := seen[c]; ok {
			t.Errorf("duplicate code %q at indices %d and %d", c, prev, i)
		}
		seen[c] = i
	}
}
