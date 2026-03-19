package notebooklm

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/openmodu/modu/pkg/playwright"
	"github.com/openmodu/modu/repos/notebooklm/rpc"
	pw "github.com/playwright-community/playwright-go"
)

// Login performs browser-based Google authentication using persistent context
func Login() error {
	fmt.Fprintln(os.Stderr, "Opening browser for Google login...")
	fmt.Fprintln(os.Stderr, "Please sign in to your Google account.")

	// Ensure directories exist
	storageDir := GetStorageDir()
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}

	browserProfileDir := GetBrowserProfileDir()
	if err := os.MkdirAll(browserProfileDir, 0700); err != nil {
		return fmt.Errorf("failed to create browser profile directory: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Using persistent profile: %s\n", browserProfileDir)

	// Create persistent context (like Python's launch_persistent_context)
	pctx, err := playwright.LaunchPersistentContext(
		browserProfileDir,
		playwright.WithHeadless(false),
		playwright.WithBrowserType("chromium"),
	)
	if err != nil {
		return fmt.Errorf("failed to create browser: %w", err)
	}
	defer pctx.Close()

	// Get or create page using raw playwright types
	var page pw.Page
	pages := pctx.Pages()
	if len(pages) > 0 {
		page = pages[0]
	} else {
		page, err = pctx.Raw().NewPage()
		if err != nil {
			return fmt.Errorf("failed to create page: %w", err)
		}
	}

	// Navigate to NotebookLM
	_, err = page.Goto(rpc.BaseURL, pw.PageGotoOptions{
		WaitUntil: pw.WaitUntilStateNetworkidle,
	})
	if err != nil {
		return fmt.Errorf("failed to navigate: %w", err)
	}

	fmt.Fprintln(os.Stderr, "\nInstructions:")
	fmt.Fprintln(os.Stderr, "1. Complete the Google login in the browser window")
	fmt.Fprintln(os.Stderr, "2. Wait until you see the NotebookLM homepage")
	fmt.Fprintln(os.Stderr, "3. The browser will close automatically once logged in")

	// Wait for successful login by checking for NotebookLM-specific elements
	maxWait := 5 * time.Minute
	pollInterval := 2 * time.Second
	start := time.Now()

	for time.Since(start) < maxWait {
		// Check current URL
		currentURL := page.URL()

		// If we're on the main NotebookLM page (not accounts.google.com), we're logged in
		if isLoggedInURL(currentURL) {
			// Verify by checking for CSRF token in page source
			content, err := page.Content()
			if err == nil {
				if _, err := ExtractCSRFToken(content); err == nil {
					fmt.Fprintln(os.Stderr, "Login successful!")

					// Save storage state using Playwright's method
					storagePath := GetStoragePath()
					if err := pctx.StorageState(storagePath); err != nil {
						return fmt.Errorf("failed to save storage state: %w", err)
					}

					// Set restrictive permissions
					if err := os.Chmod(storagePath, 0600); err != nil {
						return fmt.Errorf("failed to set file permissions: %w", err)
					}

					fmt.Fprintf(os.Stderr, "Credentials saved to %s\n", storagePath)
					return nil
				}
			}
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("login timed out after %v", maxWait)
}

// isLoggedInURL checks if the URL indicates successful login
func isLoggedInURL(url string) bool {
	// Check if we're on the main NotebookLM page
	if len(url) < len(rpc.BaseURL) {
		return false
	}

	// Should be on notebooklm.google.com, not accounts.google.com
	return findSubstring(url, "notebooklm.google.com") >= 0 &&
		findSubstring(url, "accounts.google.com") < 0
}

// saveBrowserCookies extracts and saves cookies from the browser using StorageState
func saveBrowserCookies(page *playwright.Page) error {
	// Get storage state from the page's browser context
	// This uses Playwright's built-in method which captures all cookie attributes
	ctx := page.Context()

	// Save directly to file using Playwright's StorageState method
	storagePath := GetStoragePath()

	// Ensure directory exists
	dir := GetStorageDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}

	_, err := ctx.StorageState(storagePath)
	if err != nil {
		return fmt.Errorf("failed to save storage state: %w", err)
	}

	// Set restrictive permissions on the file (contains sensitive cookies)
	if err := os.Chmod(storagePath, 0600); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	return nil
}

// LoginWithExistingCookies tries to use existing cookies, falls back to interactive login
func LoginWithExistingCookies() (*Client, error) {
	// Try to load existing auth
	if StorageExists() {
		client, err := NewClientFromStorage("")
		if err == nil {
			// Verify by refreshing tokens
			if err := client.RefreshTokens(context.Background()); err == nil {
				return client, nil
			}
		}
		fmt.Fprintln(os.Stderr, "Existing session expired, need to re-login")
	}

	// No valid session, need interactive login
	if err := Login(); err != nil {
		return nil, err
	}

	return NewClientFromStorage("")
}
