package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// #2773: streaming responses flush after every chunk through
// http.NewResponseController; the status-capturing wrapper (now
