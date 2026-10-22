package fetcher

import (
	"context"
	"errors"
	"net"
)

// Error codes are stable keys the page translates into advice; the message
// keeps the technical detail for admins.
const (
	CodeNetwork  = "network"   // could not reach magma.url
	CodeTimeout  = "timeout"   // reached it, but no answer in time
	CodeAuth     = "auth"      // 401/403: the cookie is missing, expired or invalid
	CodeHTTP     = "http"      // any other unexpected status
	CodeNotImage = "not_image" // 200, but a login page or similar instead of a picture
	CodeTooLarge = "too_large" // over maxImageBytes
	CodeSave     = "save"      // the download worked, writing it to disk did not
)

var errTooManyRedirects = errors.New("stopped after 10 redirects")

type fetchError struct {
	code string
	err  error
}

func (e *fetchError) Error() string { return e.err.Error() }
func (e *fetchError) Unwrap() error { return e.err }

func fail(code string, err error) error { return &fetchError{code, err} }

// transportCode tells a timeout from other failures to talk to the server.
func transportCode(err error) string {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return CodeTimeout
	}
	return CodeNetwork
}

// ErrorCode returns the code of an error from a fetch run.
func ErrorCode(err error) string {
	var fe *fetchError
	if errors.As(err, &fe) {
		return fe.code
	}
	return CodeSave // only local file operations return unclassified errors
}
