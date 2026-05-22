package coding_agent

import "strings"

func isDangerousBashCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if hasShellWriteRedirection(command) {
		return true
	}
	for _, segment := range splitBashSegments(command) {
		if isDangerousBashSegment(segment) {
			return true
		}
	}
	return false
}

func hasShellWriteRedirection(command string) bool {
	for i, r := range command {
		if r != '>' {
			continue
		}
		if i > 0 && command[i-1] == '<' {
			continue
		}
		return true
	}
	return strings.Contains(command, "<<")
}

func splitBashSegments(command string) []string {
	return strings.FieldsFunc(command, func(r rune) bool {
		switch r {
		case ';', '|', '&', '\n':
			return true
		default:
			return false
		}
	})
}

func isDangerousBashSegment(segment string) bool {
	words := strings.Fields(strings.TrimSpace(segment))
	words = trimShellPrefixes(words)
	if len(words) == 0 {
		return false
	}
	cmd := strings.Trim(words[0], `"'`)
	switch cmd {
	case "rm", "rmdir", "mv", "cp", "mkdir", "touch", "truncate", "tee", "dd", "install", "rsync", "chmod", "chown", "chgrp", "ln", "unlink":
		return true
	case "bash", "sh", "zsh", "fish":
		return true
	case "curl", "wget":
		return hasShortFlag(words[1:], "o") || hasShortFlag(words[1:], "O") || hasLongFlag(words[1:], "output")
	case "sed":
		return hasShortFlag(words[1:], "i")
	case "perl", "ruby":
		return hasShortFlag(words[1:], "i")
	case "git":
		return isDangerousGitCommand(words[1:])
	case "go":
		return isDangerousGoCommand(words[1:])
	case "npm", "pnpm", "yarn", "bun":
		return isDangerousPackageCommand(words[1:])
	default:
		return false
	}
}

func trimShellPrefixes(words []string) []string {
	for len(words) > 0 {
		word := strings.Trim(words[0], `"'`)
		if strings.Contains(word, "=") && !strings.HasPrefix(word, "-") {
			words = words[1:]
			continue
		}
		switch word {
		case "sudo", "env", "command", "builtin", "time", "nohup":
			words = words[1:]
			continue
		default:
			return words
		}
	}
	return words
}

func hasShortFlag(words []string, flag string) bool {
	for _, word := range words {
		word = strings.Trim(word, `"'`)
		if word == "--" {
			return false
		}
		if word == "-"+flag || strings.HasPrefix(word, "-"+flag) {
			return true
		}
	}
	return false
}

func hasLongFlag(words []string, flag string) bool {
	for _, word := range words {
		word = strings.Trim(word, `"'`)
		if word == "--" {
			return false
		}
		if word == "--"+flag || strings.HasPrefix(word, "--"+flag+"=") {
			return true
		}
	}
	return false
}

func firstNonFlag(words []string) string {
	for _, word := range words {
		word = strings.Trim(word, `"'`)
		if word == "" || strings.HasPrefix(word, "-") {
			continue
		}
		return word
	}
	return ""
}

func isDangerousGitCommand(words []string) bool {
	switch firstNonFlag(words) {
	case "am", "apply", "checkout", "cherry-pick", "clean", "commit", "merge", "mv", "rebase", "reset", "restore", "revert", "rm", "stash", "switch":
		return true
	default:
		return false
	}
}

func isDangerousGoCommand(words []string) bool {
	subcommand := firstNonFlag(words)
	switch subcommand {
	case "generate", "get", "install":
		return true
	case "mod":
		switch firstNonFlag(words[1:]) {
		case "edit", "init", "tidy", "vendor":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func isDangerousPackageCommand(words []string) bool {
	switch firstNonFlag(words) {
	case "add", "ci", "install", "link", "remove", "uninstall", "unlink", "update", "upgrade":
		return true
	default:
		return false
	}
}
