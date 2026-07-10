package sshauthz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const AuthorizePath = "/v1/ssh/authorize"

// HTTPAuthorizer speaks the generated grpc-gateway JSON contract. Sign is the
