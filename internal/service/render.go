package service

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (s *Service) render(w http.ResponseWriter, data any, status int) error {

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("error encoding response %v: %w", data, err)
	}
	w.WriteHeader(status)
	w.Header().Add("Content-Type", "application/json")

	if _, err := w.Write(jsonBytes); err != nil {
		return fmt.Errorf("error writing response: %w", err)
	}

	return nil
}
