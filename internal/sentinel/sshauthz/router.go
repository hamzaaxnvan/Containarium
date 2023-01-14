package sshauthz

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tg123/sshpiper/libplugin"
	"github.com/tg123/sshpiper/libplugin/skel"
	"gopkg.in/yaml.v3"
)

// Router returns the upstream sshpiper's yaml plugin would return for an
// offered public key or certificate. It runs the yaml plugin's own checker,
// libplugin/skel (certificate validity, CA match, authorized_keys match and
// upstream construction), over the config.yaml keysync generates, so enforce
// mode can route a connection itself instead of deferring to yaml (#2629).
type Router struct {
	path string
	skel *skel.SkelPlugin
}

func NewRouter(configPath string) *Router {
	r := &Router{path: configPath}
	r.skel = skel.NewSkelPlugin(r.listPipes)
	return r
}

// Route returns the upstream, or an error when no pipe verifies the key. Like
// the yaml plugin, it re-reads config.yaml on every call.
func (r *Router) Route(conn libplugin.ConnMetadata, publicKey []byte) (*libplugin.Upstream, error) {
	return r.skel.PublicKeyCallback(conn, publicKey)
