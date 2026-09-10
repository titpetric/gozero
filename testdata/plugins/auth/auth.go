// Package auth parses Authorization headers into typed credentials.
package auth

import (
	"errors"
	"strings"
	"hostlog"
)

type Credentials struct {
	Scheme string `json:"scheme"`
	Token  string `json:"token"`
}

func ParseHeader(header string) (*Credentials, error) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if ok && token != "" {
		return &Credentials{Scheme: "bearer", Token: token}, nil
	}
	if header == "" {
		return nil, errors.New("missing authorization header")
	}
	return nil, errors.New("unsupported authorization scheme")
}

func init() {
	hostlog.Loaded("auth")
}
