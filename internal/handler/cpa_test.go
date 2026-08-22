package handler

import (
	"net/http"
	"testing"
	"time"
)

type deadlineRecordingWriter struct {
	deadline time.Time
}

func (w *deadlineRecordingWriter) Header() http.Header { return make(http.Header) }

func (w *deadlineRecordingWriter) Write(p []byte) (int, error) { return len(p), nil }

func (w *deadlineRecordingWriter) WriteHeader(int) {}

func (w *deadlineRecordingWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestClearLongRunningResponseDeadline(t *testing.T) {
	w := &deadlineRecordingWriter{deadline: time.Now().Add(time.Minute)}
	clearLongRunningResponseDeadline(w)
	if !w.deadline.IsZero() {
		t.Fatalf("write deadline = %v, want zero deadline", w.deadline)
	}
}
