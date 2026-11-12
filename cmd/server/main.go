package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jakob/simple-vocabulary/internal/accounts"
	"github.com/jakob/simple-vocabulary/internal/ai"
	"github.com/jakob/simple-vocabulary/internal/api"
	"github.com/jakob/simple-vocabulary/internal/conjugation"
	"github.com/jakob/simple-vocabulary/internal/dictionary"
	"github.com/jakob/simple-vocabulary/internal/mail"
	"github.com/jakob/simple-vocabulary/internal/shared"
)

func main() {
	root := findRoot()
	dictPath := envOr("DICT_PATH", filepath.Join(root, "data", "spa-deu.json"))
	conjPath := envOr("CONJ_PATH", filepath.Join(root, "data", "spa-conj.json"))
	webDir := envOr("WEB_DIR", filepath.Join(root, "web"))
	initialPath := envOr("INITIAL_PATH", filepath.Join(root, "data", "simple-vocabulary-initial.json"))
	sharedDir := envOr("SHARED_DIR", filepath.Join(root, "data", "shared"))
	accountsDir := envOr("ACCOUNTS_DIR", filepath.Join(root, "data", "accounts"))

	dict, err := dictionary.LoadJSON(dictPath)
	if err != nil {
		log.Fatalf("load dictionary: %v", err)
	}
	log.Printf("dictionary loaded: %d entries (%s→%s)", dict.Size(), dict.SourceLang, dict.TargetLang)

	conj, err := conjugation.LoadJSON(conjPath)
	if err != nil {
		log.Fatalf("load conjugations: %v", err)
	}
	log.Printf("conjugations loaded: %d verbs", conj.Size())

	var provider ai.Provider
	if or, err := ai.NewOpenRouterFromEnv(); err != nil {
		log.Printf("AI disabled: %v", err)
	} else {
		provider = or
		log.Printf("AI provider: %s", provider.Name())
	}

	shareStore, err := shared.NewStore(sharedDir)
	if err != nil {
		log.Fatalf("shared store: %v", err)
	}
	log.Printf("shared sets: %s", sharedDir)

	accountStore, err := accounts.NewStore(accountsDir)
	if err != nil {
		log.Fatalf("accounts store: %v", err)
	}
	mailer := mail.FromEnv()
	log.Printf("accounts: %s (mail: %s)", accountsDir, mailer.Name())

	srv := api.New(dict, provider, webDir)
	srv.Conj = conj
	srv.InitialPath = initialPath
	srv.Shared = shareStore
	srv.Accounts = accountStore
	srv.Mail = mailer
	if _, err := os.Stat(initialPath); err != nil {
		log.Printf("initial configuration not found: %s", initialPath)
	} else {
		log.Printf("initial configuration: %s", initialPath)
	}
	addr := api.AddrFromEnv()
	log.Printf("listening on %s (web: %s)", addr, webDir)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func findRoot() string {
	if v := os.Getenv("APP_ROOT"); v != "" {
		return v
	}
	// Walk up from executable / cwd looking for data/spa-deu.json or go.mod.
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := cwd
	for i := 0; i < 6; i++ {
		if fileExists(filepath.Join(dir, "go.mod")) || fileExists(filepath.Join(dir, "data", "spa-deu.json")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
