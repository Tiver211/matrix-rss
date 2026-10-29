package domain

import "errors"

var (
	// ErrFeedNotFound is returned when a room has no feed or a feed token does not work.
	ErrFeedNotFound = errors.New("feed not found")
	// ErrMessageNotFound is returned when a message is not stored.
	ErrMessageNotFound = errors.New("message not found")
	// ErrCredentialNotFound is returned when an API key or feed token ID is unknown.
	ErrCredentialNotFound = errors.New("credential not found")
	// ErrInvalidCredentials is returned when a secret is unknown, revoked or expired.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrMediaUnavailable is returned when a message has no file or the homeserver no longer has it.
	ErrMediaUnavailable = errors.New("media not available")
	// ErrMediaTooLarge is returned when a file is bigger than the configured limit.
	ErrMediaTooLarge = errors.New("media too large")
	// ErrInvalid marks a rejected request; the wrapping error says why.
	ErrInvalid = errors.New("invalid request")
)
