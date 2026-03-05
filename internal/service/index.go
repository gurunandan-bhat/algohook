package service

import (
	"net/http"
)

type Data struct {
	Message string `json:"message,omitempty"`
}

func (s *Service) index(w http.ResponseWriter, r *http.Request) error {

	return s.render(w, Data{"Hello, World!"}, http.StatusOK)
}
