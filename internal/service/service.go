package service

import (
	"algohook/internal/config"
	"algohook/internal/model"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type Service struct {
	Config *config.Config
	Muxer  *chi.Mux
	Model  *model.Model
	Logger *slog.Logger
}

// var csrfProtector *http.CrossOriginProtection

func NewService(cfg *config.Config) (*Service, error) {

	mux := chi.NewRouter()

	mux.Use(middleware.Recoverer)
	mux.Use(middleware.RequestID)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mux.Use(slogMiddleware(cfg, logger))

	mux.Use(middleware.RealIP)

	mux.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))

	m, err := model.NewModel(cfg)
	if err != nil {
		log.Fatalf("error connecting to database: %s", err)
	}

	// Static file handler
	// IMP: This should come *AFTER* all
	// middleware are set via mux.Use
	filesDir := http.Dir(filepath.Join(cfg.AppRoot, "ai-assets"))
	fs := http.FileServer(filesDir)
	mux.Handle("/ai-assets/*", http.StripPrefix("/ai-assets", fs))

	s := &Service{
		Config: cfg,
		Muxer:  mux,
		Model:  m,
		Logger: logger,
	}

	s.setRoutes()

	return s, nil
}

func (s *Service) setRoutes() {

	s.Muxer.Method(http.MethodGet, "/", serviceHandler(s.index))

	// This is the handler for APIs that respond with a JSON response
	// that handles error rather than returning it
}
