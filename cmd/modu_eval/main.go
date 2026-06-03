package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/openmodu/modu/pkg/evals"
)

var (
	filename         string
	showOnlyFailures bool
	plainView        bool
	showOutput       bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "modu_eval",
		Short: "Run and view modu LLM eval results",
		Long: `modu_eval runs Go eval tests with GOEVALS=1 and displays evals.jsonl.

Environment:
  EVAL_PROVIDER      Provider id. Supports comma-separated values. Default: lmstudio
  EVAL_BASE_URL      OpenAI-compatible base URL. Default depends on provider.
  EVAL_API_KEY       API key for the eval provider.
  EVAL_MODEL         Model under test. Required.
  GRADER_PROVIDER    Grader provider. Defaults to the eval provider.
  GRADER_BASE_URL    Grader OpenAI-compatible base URL.
  GRADER_API_KEY     Grader API key.
  GRADER_MODEL       Grader model. Defaults to EVAL_MODEL.`,
	}

	runCmd := &cobra.Command{
		Use:                "run [go test flags and args]",
		Short:              "Run eval tests and open the TUI",
		DisableFlagParsing: true,
		Example: `  modu_eval run -v ./pkg/agent -run Eval
  modu_eval run -v ./...`,
		Run: func(cmd *cobra.Command, args []string) {
			runCommand(args)
		},
	}

	viewCmd := &cobra.Command{
		Use:     "view",
		Short:   "Display existing evaluation results",
		Example: "  modu_eval view -f evals.jsonl\n  modu_eval view --plain --output",
		Run: func(cmd *cobra.Command, args []string) {
			viewCommand()
		},
	}
	viewCmd.Flags().StringVarP(&filename, "file", "f", "evals.jsonl", "path to evals.jsonl")
	viewCmd.Flags().BoolVar(&showOnlyFailures, "failures-only", false, "show only failed evals")
	viewCmd.Flags().BoolVar(&plainView, "plain", false, "print a plain text report instead of the TUI")
	viewCmd.Flags().BoolVar(&showOutput, "output", false, "include output excerpts in plain text reports")

	checkCmd := &cobra.Command{
		Use:                "check [go test flags and args]",
		Short:              "Run eval tests and print a CI-friendly summary",
		DisableFlagParsing: true,
		Example: `  modu_eval check -v ./pkg/agent -run Eval
  modu_eval check -v ./...`,
		Run: func(cmd *cobra.Command, args []string) {
			checkCommand(args)
		},
	}

	commentCmd := &cobra.Command{
		Use:                "comment [go test flags and args]",
		Short:              "Run eval tests and write a GitHub comment to comment.md",
		DisableFlagParsing: true,
		Example:            "  modu_eval comment -v ./pkg/agent -run Eval",
		Run: func(cmd *cobra.Command, args []string) {
			commentCommand(args)
		},
	}

	rootCmd.AddCommand(runCmd, viewCmd, checkCmd, commentCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runCommand(args []string) {
	testErr := runEvalTests(args)
	if testErr != nil {
		fmt.Fprintf(os.Stderr, "\ngo test completed with errors: %v\n", testErr)
	} else {
		fmt.Println("\nTests completed successfully.")
	}

	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		fmt.Println("You can view results manually with: modu_eval view -f /path/to/evals.jsonl")
		os.Exit(1)
	}

	results, err := loadResults(evalFile, false)
	if err != nil {
		exitErr(err)
	}
	if err := runTUI(results); err != nil {
		exitErr(err)
	}
}

func viewCommand() {
	results, err := loadResults(filename, showOnlyFailures)
	if err != nil {
		exitErr(err)
	}
	if plainView {
		printReport(results, reportOptions{ShowDetails: true, ShowOutput: showOutput})
		return
	}
	if err := runTUI(results); err != nil {
		exitErr(err)
	}
}

func checkCommand(args []string) {
	testErr := runEvalTests(args)
	if testErr != nil {
		fmt.Fprintf(os.Stderr, "\ngo test completed with errors: %v\n", testErr)
	} else {
		fmt.Println("\nTests completed successfully.")
	}

	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		fmt.Println("No evaluation results found to check.")
		if testErr != nil {
			os.Exit(1)
		}
		return
	}

	results, err := loadResults(evalFile, false)
	if err != nil {
		exitErr(err)
	}
	printSummary(results)
	if hasFailures(results) || testErr != nil {
		os.Exit(1)
	}
}

func commentCommand(args []string) {
	_ = runEvalTests(args)

	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		if err := os.WriteFile("comment.md", []byte(""), 0644); err != nil {
			exitErr(err)
		}
		return
	}

	results, err := loadResults(evalFile, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
		if err := os.WriteFile("comment.md", []byte(""), 0644); err != nil {
			exitErr(err)
		}
		return
	}

	comment := generateGitHubComment(results)
	if err := os.WriteFile("comment.md", []byte(comment), 0644); err != nil {
		exitErr(err)
	}
	fmt.Printf("\nGenerated comment (%d lines) -> comment.md\n", len(strings.Split(comment, "\n")))
}

func runEvalTests(args []string) error {
	if err := cleanupOldResults(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to clean old results: %v\n", err)
	}
	if len(args) == 0 {
		args = []string{"./..."}
	}

	fmt.Println("Running evaluations...")
	cmdArgs := append([]string{"test"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Env = append(os.Environ(), "GOEVALS=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cleanupOldResults() error {
	root, err := evals.FindModuleRoot("")
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(root, "evals.jsonl"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func findEvalsFile() (string, error) {
	root, err := evals.FindModuleRoot("")
	if err != nil {
		return "", err
	}
	file := filepath.Join(root, "evals.jsonl")
	if _, err := os.Stat(file); err != nil {
		return "", err
	}
	return file, nil
}

func loadResults(filename string, failuresOnly bool) ([]evals.EvalLogLine, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 20*1024*1024)

	var results []evals.EvalLogLine
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var result evals.EvalLogLine
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			fmt.Fprintf(os.Stderr, "skip invalid result line: %v\n", err)
			continue
		}
		if failuresOnly && result.Pass {
			continue
		}
		results = append(results, result)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func printSummary(results []evals.EvalLogLine) {
	printReport(results, reportOptions{ShowDetails: false})
}

type reportOptions struct {
	ShowDetails bool
	ShowOutput  bool
}

func printReport(results []evals.EvalLogLine, opts reportOptions) {
	if len(results) == 0 {
		fmt.Println("No evaluation results.")
		return
	}

	type stats struct {
		passed int
		failed int
		score  float64
	}
	byProvider := map[string]stats{}
	byTest := map[string]stats{}
	for _, result := range results {
		providerKey := providerLabel(result)
		stat := byProvider[providerKey]
		if result.Pass {
			stat.passed++
		} else {
			stat.failed++
		}
		stat.score += result.Score
		byProvider[providerKey] = stat

		testKey := result.Name
		testStat := byTest[testKey]
		if result.Pass {
			testStat.passed++
		} else {
			testStat.failed++
		}
		testStat.score += result.Score
		byTest[testKey] = testStat
	}

	totalPassed := 0
	totalFailed := 0
	totalScore := 0.0
	fmt.Println("\nEvaluation Results")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("By Provider")
	for _, provider := range sortedKeys(byProvider) {
		stat := byProvider[provider]
		total := stat.passed + stat.failed
		totalPassed += stat.passed
		totalFailed += stat.failed
		totalScore += stat.score
		fmt.Printf("%-36s %4d total  %4d passed  %4d failed  avg %.2f\n", provider, total, stat.passed, stat.failed, avg(stat.score, total))
	}
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("%-36s %4d total  %4d passed  %4d failed  avg %.2f\n", "overall", totalPassed+totalFailed, totalPassed, totalFailed, avg(totalScore, totalPassed+totalFailed))

	fmt.Println("\nBy Test")
	fmt.Println(strings.Repeat("=", 80))
	for _, test := range sortedKeys(byTest) {
		stat := byTest[test]
		total := stat.passed + stat.failed
		status := "PASS"
		if stat.failed > 0 {
			status = "FAIL"
		}
		fmt.Printf("[%s] %3d/%-3d avg %.2f  %s\n", status, stat.passed, total, avg(stat.score, total), test)
	}

	if totalFailed == 0 {
		if opts.ShowDetails {
			printDetails(results, opts)
		}
		return
	}

	fmt.Println("\nFailed Evaluations")
	fmt.Println(strings.Repeat("=", 80))
	for i, result := range sortedResults(results) {
		if result.Pass {
			continue
		}
		fmt.Printf("%d. %s\n", i+1, result.Name)
		fmt.Printf("   rubric: %s\n", truncate(result.Rubric, 120))
		fmt.Printf("   score:  %.2f\n", result.Score)
		if result.Reasoning != "" {
			fmt.Printf("   reason: %s\n", truncate(result.Reasoning, 300))
		}
	}

	if opts.ShowDetails {
		printDetails(results, opts)
	}
}

func printDetails(results []evals.EvalLogLine, opts reportOptions) {
	fmt.Println("\nRubric Details")
	fmt.Println(strings.Repeat("=", 80))
	for i, result := range sortedResults(results) {
		status := "PASS"
		if !result.Pass {
			status = "FAIL"
		}
		fmt.Printf("%d. [%s] score %.2f  %s\n", i+1, status, result.Score, result.Name)
		fmt.Printf("   provider: %s\n", providerLabel(result))
		if result.Grader != "" {
			fmt.Printf("   grader:   %s\n", result.Grader)
		}
		fmt.Printf("   rubric:   %s\n", truncate(result.Rubric, 240))
		if result.Reasoning != "" {
			fmt.Printf("   reason:   %s\n", truncate(result.Reasoning, 360))
		}
		if opts.ShowOutput && result.Output != "" {
			fmt.Printf("   output:   %s\n", oneLine(truncate(result.Output, 600)))
		}
	}
}

func generateGitHubComment(results []evals.EvalLogLine) string {
	if len(results) == 0 {
		return "No evaluation results found."
	}

	byProvider := map[string]struct {
		passed   int
		failed   int
		failures []evals.EvalLogLine
	}{}
	totalPassed := 0
	totalFailed := 0

	for _, result := range results {
		key := providerLabel(result)
		stat := byProvider[key]
		if result.Pass {
			stat.passed++
			totalPassed++
		} else {
			stat.failed++
			totalFailed++
			stat.failures = append(stat.failures, result)
		}
		byProvider[key] = stat
	}

	total := totalPassed + totalFailed
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Overall: %d/%d evals passed (%.1f%%)**\n\n", totalPassed, total, avg(float64(totalPassed)*100, total)))
	b.WriteString("| Provider | Total | Passed | Failed | Pass Rate |\n")
	b.WriteString("|----------|-------|--------|--------|----------|\n")
	for _, provider := range sortedKeys(byProvider) {
		stat := byProvider[provider]
		providerTotal := stat.passed + stat.failed
		b.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %.1f%% |\n", provider, providerTotal, stat.passed, stat.failed, avg(float64(stat.passed)*100, providerTotal)))
	}

	if totalFailed > 0 {
		b.WriteString("\n### Failed Evaluations\n\n")
		b.WriteString("<details>\n")
		b.WriteString(fmt.Sprintf("<summary>Show %d failures</summary>\n\n", totalFailed))
		count := 1
		for _, provider := range sortedKeys(byProvider) {
			stat := byProvider[provider]
			if len(stat.failures) == 0 {
				continue
			}
			b.WriteString(fmt.Sprintf("#### %s\n\n", provider))
			for _, failure := range sortedResults(stat.failures) {
				b.WriteString(fmt.Sprintf("**%d. %s**\n\n", count, failure.Name))
				b.WriteString(fmt.Sprintf("- **Score:** %.2f\n", failure.Score))
				b.WriteString(fmt.Sprintf("- **Rubric:** %s\n", truncate(failure.Rubric, 150)))
				if failure.Reasoning != "" {
					b.WriteString(fmt.Sprintf("- **Reason:** %s\n", truncate(failure.Reasoning, 300)))
				}
				b.WriteString("\n")
				count++
			}
		}
		b.WriteString("</details>\n")
	}
	return b.String()
}

func hasFailures(results []evals.EvalLogLine) bool {
	for _, result := range results {
		if !result.Pass {
			return true
		}
	}
	return false
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max-3] + "..."
}

func providerLabel(result evals.EvalLogLine) string {
	provider := result.Provider
	if provider == "" {
		provider = "unknown"
	}
	if result.Model == "" {
		return provider
	}
	return provider + "/" + result.Model
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedResults(results []evals.EvalLogLine) []evals.EvalLogLine {
	out := append([]evals.EvalLogLine(nil), results...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].RunNumber != out[j].RunNumber {
			return out[i].RunNumber < out[j].RunNumber
		}
		return out[i].Rubric < out[j].Rubric
	})
	return out
}

func avg(total float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
