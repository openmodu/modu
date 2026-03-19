package notebooklm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

const (
	defaultStorageDir  = ".notebooklm"
	storageFileName    = "storage_state.json"
	envAuthJSON        = "NOTEBOOKLM_AUTH_JSON"
	envNotebookLMHome  = "NOTEBOOKLM_HOME"
)

// PlaywrightStorageState represents Playwright's storage state format
type PlaywrightStorageState struct {
	Cookies []PlaywrightCookie `json:"cookies"`
}

// PlaywrightCookie represents a cookie in Playwright format
type PlaywrightCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

// LoadAuthTokens loads authentication tokens from storage
func LoadAuthTokens(storagePath string) (*vo.AuthTokens, error) {
	var data []byte
	var err error

	// Priority 1: Explicit path
	if storagePath != "" {
		data, err = os.ReadFile(storagePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read storage file: %w", err)
		}
	} else if envJSON := os.Getenv(envAuthJSON); envJSON != "" {
		// Priority 2: Environment variable (inline JSON)
		data = []byte(envJSON)
	} else {
		// Priority 3: Default location
		path := getDefaultStoragePath()
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("no auth found at %s: %w", path, err)
		}
	}

	return parseStorageState(data)
}

// getDefaultStoragePath returns the default storage file path
func getDefaultStoragePath() string {
	if home := os.Getenv(envNotebookLMHome); home != "" {
		return filepath.Join(home, storageFileName)
	}

	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, defaultStorageDir, storageFileName)
}

// GetStorageDir returns the storage directory path
func GetStorageDir() string {
	if home := os.Getenv(envNotebookLMHome); home != "" {
		return home
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, defaultStorageDir)
}

// GetStoragePath returns the full storage file path
func GetStoragePath() string {
	return filepath.Join(GetStorageDir(), storageFileName)
}

// GetBrowserProfileDir returns the browser profile directory path
func GetBrowserProfileDir() string {
	return filepath.Join(GetStorageDir(), "browser_profile")
}

// parseStorageState parses Playwright storage state JSON
func parseStorageState(data []byte) (*vo.AuthTokens, error) {
	var state PlaywrightStorageState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse storage state: %w", err)
	}

	if len(state.Cookies) == 0 {
		return nil, errors.New("no cookies found in storage state")
	}

	// Extract cookies for allowed domains
	// Include all Google-related domains for proper cross-domain authentication
	cookies := make(map[string]string)
	var cookiesWithDomain []vo.CookieWithDomain

	for _, cookie := range state.Cookies {
		// Accept cookies from any google.com subdomain or .google.com wildcard
		if isGoogleDomain(cookie.Domain) {
			cookies[cookie.Name] = cookie.Value
			// Also store with domain info for cross-domain downloads
			cookiesWithDomain = append(cookiesWithDomain, vo.CookieWithDomain{
				Name:   cookie.Name,
				Value:  cookie.Value,
				Domain: cookie.Domain,
			})
		}
	}

	if len(cookies) == 0 {
		return nil, errors.New("no valid Google cookies found")
	}

	return &vo.AuthTokens{
		Cookies:           cookies,
		CookiesWithDomain: cookiesWithDomain,
	}, nil
}

// ExtractCSRFToken extracts SNlM0e token from HTML
func ExtractCSRFToken(html string) (string, error) {
	re := regexp.MustCompile(`"SNlM0e"\s*:\s*"([^"]+)"`)
	matches := re.FindStringSubmatch(html)
	if len(matches) < 2 {
		return "", errors.New("CSRF token not found in page")
	}
	return matches[1], nil
}

// ExtractSessionID extracts FdrFJe session ID from HTML
func ExtractSessionID(html string) (string, error) {
	re := regexp.MustCompile(`"FdrFJe"\s*:\s*"([^"]+)"`)
	matches := re.FindStringSubmatch(html)
	if len(matches) < 2 {
		return "", errors.New("session ID not found in page")
	}
	return matches[1], nil
}

// SaveStorageState saves Playwright storage state to file
func SaveStorageState(cookies []PlaywrightCookie) error {
	dir := GetStorageDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}

	state := PlaywrightStorageState{Cookies: cookies}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal storage state: %w", err)
	}

	path := GetStoragePath()
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write storage file: %w", err)
	}

	return nil
}

// StorageExists checks if storage file exists
func StorageExists() bool {
	_, err := os.Stat(GetStoragePath())
	return err == nil
}

// isGoogleDomain checks if a domain is a Google-related domain for NotebookLM
// Note: YouTube cookies are excluded as they have duplicate cookie names (HSID, SSID, etc.)
// that conflict with the .google.com cookies needed for NotebookLM API calls
func isGoogleDomain(domain string) bool {
	// Exclude YouTube domains - they have conflicting cookie names
	if domain == ".youtube.com" || domain == "youtube.com" ||
		strings.HasSuffix(domain, ".youtube.com") {
		return false
	}

	googleDomains := []string{
		".google.com",
		"google.com",
		".googleusercontent.com",
		"googleusercontent.com",
	}

	for _, gd := range googleDomains {
		if domain == gd || strings.HasSuffix(domain, gd) {
			return true
		}
		// Also match subdomains like "accounts.google.com"
		if strings.HasSuffix(domain, "."+strings.TrimPrefix(gd, ".")) {
			return true
		}
	}
	return false
}
