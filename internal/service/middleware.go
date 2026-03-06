package service

import (
	"algohook/internal/webhook"
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Middleware func(serviceHandler) serviceHandler

func (s *Service) validateGithubPush(next serviceHandler) serviceHandler {

	return func(w http.ResponseWriter, r *http.Request) error {

		sigHdr := r.Header.Get("X-Hub-Signature-256")
		if sigHdr == "" {
			return errors.New("invalid signature")
		}

		sig := strings.TrimPrefix(sigHdr, "sha256=")
		sigExp, err := hex.DecodeString(sig)
		if err != nil {
			return fmt.Errorf("error decoding signature string: %w", err)
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			return fmt.Errorf("error reading request body: %w", err)
		}

		sigGot := webhook.GenerateHMAC(s.Config.API.Key, bodyBytes)
		if !hmac.Equal(sigExp, sigGot) {
			return errors.New("signature match failed")
		}
		if err := r.Body.Close(); err != nil {
			return fmt.Errorf("error closing request body: %w", err)
		}

		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		w.Header().Add("Cache-Control", "no-store")

		next.ServeHTTP(w, r)

		return nil
	}
}

// serviceHandler(Chain(s.index, s.validateGithubPush, s.requireJSON))

func Chain(h serviceHandler, middleware ...Middleware) serviceHandler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
