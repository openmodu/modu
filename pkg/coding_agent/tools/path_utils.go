package tools

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ExpandPath expands ~ to the user's home directory and normalizes Unicode.
func ExpandPath(filePath string) string {
	// Replace Unicode whitespace with regular spaces
	var b strings.Builder
	for _, r := range filePath {
		if unicode.IsSpace(r) && r != ' ' && r != '\t' && r != '\n' {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	filePath = b.String()

	// Strip surrounding quotes
	filePath = strings.TrimSpace(filePath)
	if len(filePath) >= 2 {
		if (filePath[0] == '"' && filePath[len(filePath)-1] == '"') ||
			(filePath[0] == '\'' && filePath[len(filePath)-1] == '\'') {
			filePath = filePath[1 : len(filePath)-1]
		}
	}

	// Expand ~
	if strings.HasPrefix(filePath, "~/") || filePath == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if filePath == "~" {
				filePath = home
			} else {
				filePath = filepath.Join(home, filePath[2:])
			}
		}
	}

	return filePath
}

// ResolveToCwd resolves a file path relative to the given working directory.
func ResolveToCwd(filePath, cwd string) string {
	filePath = ExpandPath(filePath)
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}
	return filepath.Clean(filepath.Join(cwd, filePath))
}

// ResolveReadPath resolves a path for reading, handling macOS NFD normalization
// and quoted paths. Returns the resolved absolute path or an error.
func ResolveReadPath(filePath, cwd string) (string, error) {
	resolved := ResolveToCwd(filePath, cwd)

	// Try the path directly first
	if _, err := os.Stat(resolved); err == nil {
		return resolved, nil
	}

	// On macOS, try NFC normalization (macOS filesystem uses NFD)
	nfcPath := norm.NFC.String(resolved)
	if _, err := os.Stat(nfcPath); err == nil {
		return nfcPath, nil
	}

	// Try NFD normalization
	nfdPath := norm.NFD.String(resolved)
	if _, err := os.Stat(nfdPath); err == nil {
		return nfdPath, nil
	}

	// Return the original resolved path (caller will get the stat error)
	return resolved, nil
}
