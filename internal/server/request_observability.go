package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/systemevent"
)

type observedResponseWriter struct {
	http.ResponseWriter
	status       int
	errorCode    string
	errorMessage string
	errorCause   error
}

func (writer *observedResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *observedResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	if status >= http.StatusInternalServerError && writer.Header().Get("X-Request-ID") == "" {
		if requestID, err := id.New(); err == nil {
			writer.Header().Set("X-Request-ID", requestID)
		}
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *observedResponseWriter) Write(value []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(value)
}

func (writer *observedResponseWriter) Flush() {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	_ = http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *observedResponseWriter) recordAPIError(code, message string, cause error) {
	writer.errorCode = code
	writer.errorMessage = message
	writer.errorCause = errors.Join(writer.errorCause, cause)
}

func observeServerErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		observed := &observedResponseWriter{ResponseWriter: response}
		next.ServeHTTP(observed, request)
		if observed.status < http.StatusInternalServerError {
			return
		}
		cause := observed.errorCause
		if cause == nil {
			message := observed.errorMessage
			if message == "" {
				message = http.StatusText(observed.status)
			}
			cause = errors.New(message)
		}
		systemevent.Failure(
			"admin_api_request_failed",
			cause,
			systemevent.String("method", request.Method),
			systemevent.String("path", request.URL.Path),
			systemevent.Int64("status", int64(observed.status)),
			systemevent.String("error_code", observed.errorCode),
			systemevent.String("request_id", observed.Header().Get("X-Request-ID")),
			systemevent.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		)
	})
}
