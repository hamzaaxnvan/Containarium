package tracker

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrCrossOriginRedirect is returned when a provider client is asked to
// follow a redirect to a different scheme or host. Following it could send
