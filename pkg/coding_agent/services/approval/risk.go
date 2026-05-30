package approval

import "strings"

// isDangerousBashCommand reports whether a bash command line performs a
// filesystem-mutating or otherwise side-effecting action that warrants approval.
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
	words := strings.Fields(command)
	for i, word := range words {
		word = strings.Trim(word, `"'`)
		if word == "" || !strings.Contains(word, ">") {
			continue
		}
		target := redirectTarget(word)
		if target == "" && i+1 < len(words) {
			target = strings.Trim(words[i+1], `"'`)
		}
		if isDiscardRedirectTarget(target) {
			continue
		}
		return true
	}
	return false
}

func redirectTarget(word string) string {
	if strings.Contains(word, "<<") {
		return ""
	}
	idx := strings.LastIndex(word, ">")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(word[idx+1:])
}

func isDiscardRedirectTarget(target string) bool {
	target = strings.Trim(target, `"'`)
	switch {
	case target == "":
		return false
	case target == "/dev/null":
		return true
	case target == "&1" || target == "&2":
		return true
	default:
		return false
	}
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
	cmd := baseShellCommand(strings.Trim(words[0], `"'`))
	switch cmd {
	case "rm", "rmdir", "mv", "cp", "mkdir", "touch", "truncate", "tee", "dd", "install", "rsync", "chmod", "chown", "chgrp", "ln", "unlink":
		return true
	case "bash", "sh", "zsh", "fish":
		return isDangerousShellWrapper(words[1:])
	case "curl", "wget":
		return hasOutputFileFlag(words[1:])
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

func baseShellCommand(cmd string) string {
	if idx := strings.LastIndex(cmd, "/"); idx >= 0 {
		return cmd[idx+1:]
	}
	return cmd
}

func isDangerousShellWrapper(words []string) bool {
	for i, word := range words {
		word = strings.Trim(word, `"'`)
		if word == "--" {
			break
		}
		if strings.HasPrefix(word, "-") && strings.Contains(word, "c") {
			if i+1 >= len(words) {
				return false
			}
			script := strings.Trim(strings.Join(words[i+1:], " "), `"'`)
			return isDangerousBashCommand(script)
		}
	}
	return false
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

func hasOutputFileFlag(words []string) bool {
	for i, word := range words {
		word = strings.Trim(word, `"'`)
		if word == "--" {
			return false
		}
		if word == "-O" {
			return true
		}
		if word == "-o" || word == "--output" {
			if i+1 >= len(words) {
				return true
			}
			return !isDiscardRedirectTarget(words[i+1])
		}
		if strings.HasPrefix(word, "-o") && word != "-o" {
			return !isDiscardRedirectTarget(strings.TrimPrefix(word, "-o"))
		}
		if strings.HasPrefix(word, "--output=") {
			return !isDiscardRedirectTarget(strings.TrimPrefix(word, "--output="))
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
