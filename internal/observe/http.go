package observe

import (
	"context"
	"net/http"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status   int
	observer Observer
	ctx      context.Context
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// HTTPHandler observes registered route templates, never raw URL paths.
func HTTPHandler(observer Observer, next http.Handler) http.Handler {
	if observer == nil {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		wrapped := &statusWriter{ResponseWriter: writer, observer: observer, ctx: request.Context()}
		next.ServeHTTP(wrapped, request)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		outcome := OutcomeOK
		if status >= 400 {
			outcome = OutcomeError
		}
		observer.Record(request.Context(), Measurement{
			Operation: OperationHTTP, Path: PathNone, Outcome: outcome,
			Route: routeTemplate(request.Pattern), Method: method(request.Method), Status: status / 100,
			Duration: time.Since(started), Items: 1,
		})
	})
}

// ResponseObserver returns the observer installed by HTTPHandler. Response
// encoding can therefore be measured without coupling service code to OTel.
func ResponseObserver(writer http.ResponseWriter) (context.Context, Observer) {
	wrapped, _ := writer.(*statusWriter)
	if wrapped == nil {
		return context.Background(), nil
	}
	return wrapped.ctx, wrapped.observer
}

func routeTemplate(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	// Patterns originate in the resident's static ServeMux registration. This
	// limit also fails closed if a future dynamic router violates that rule.
	if len(pattern) > 96 {
		return "other"
	}
	for _, character := range pattern {
		if !(character == ' ' || character == '/' || character == '{' || character == '}' || character == '-' || character == '_' || character == '.' ||
			character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9') {
			return "other"
		}
	}
	return pattern
}

func method(value string) string {
	switch value {
	case http.MethodGet, http.MethodPost, http.MethodDelete:
		return value
	default:
		return "OTHER"
	}
}
