// adversarial_demo demonstrates the Adversarial Validation pattern built on Agent Swarm:
//
//  1. A publisher pushes tasks that require validation (ValidationRequired=true).
//  2. Worker agents (cap: "text-processing") claim and process tasks.
//     When done they call SubmitForValidation instead of CompleteTask.
//  3. A validate task is automatically created and placed in the swarm queue.
//  4. Validator agents (cap: "validate") claim validate tasks, score the result,
//     and call SubmitValidation.
//  5. If the score is below the pass threshold the source task is re-queued with
//     the validator's feedback attached. Workers retry with that context.
//  6. After MaxRetries failed attempts the task is marked "failed".
//
// Run:
//
//	go run ./examples/adversarial_demo/
//
// Dashboard: http://localhost:8084
package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/mailbox/dashboard"
	"github.com/openmodu/modu/pkg/mailbox/server"
)

const mailboxAddr = "127.0.0.1:16382"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start mailbox server.
	srv := server.NewMailboxServer()
	go func() {
		if err := srv.ListenAndServe(mailboxAddr); err != nil {
			log.Printf("[main] mailbox server: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	hub := srv.Hub()

	// Start dashboard.
	dash := dashboard.NewDashboard(hub)
	go func() {
		if err := dash.Start(ctx, ":8084"); err != nil && ctx.Err() == nil {
			log.Printf("[main] dashboard: %v", err)
		}
	}()

	// Spawn 2 worker agents and 1 validator agent.
	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("worker-%d", i)
		go runWorker(ctx, id, mailboxAddr)
	}
	go runValidator(ctx, "validator-1", mailboxAddr)

	// Give agents time to register.
	time.Sleep(500 * time.Millisecond)

	// Publish tasks that require adversarial validation.
	go publishValidatedTasks(ctx)

	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Println("Adversarial Validation Demo running")
	log.Println("Dashboard: http://localhost:8084")
	log.Println("Press Ctrl+C to stop")
	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	<-ctx.Done()
	log.Println("[main] shutting down...")
}

// publishValidatedTasks publishes tasks with validation required.
// maxRetries=2, passThreshold=0.7, caps=text-processing.
func publishValidatedTasks(ctx context.Context) {
	c := client.NewMailboxClient("publisher", mailboxAddr)
	if err := c.Register(ctx); err != nil {
		log.Printf("[publisher] register: %v", err)
		return
	}
	_ = c.SetRole(ctx, "publisher")

	tasks := []string{
		"Analyse sentiment: 'The product arrived on time but the packaging was damaged.'",
		"Translate to French: 'Machine learning is transforming every industry.'",
		"Summarise in one sentence: 'Large language models are trained on vast corpora of text and can generate human-like prose.'",
		"Extract keywords from: 'transformer architecture, attention mechanism, self-supervised learning, fine-tuning'",
		"Rewrite in a formal tone: 'This thing kinda works but crashes a lot, pretty annoying tbh.'",
		"Classify as fact or opinion: 'Neural networks were inspired by the structure of the human brain.'",
	}

	for i, desc := range tasks {
		select {
		case <-ctx.Done():
			return
		default:
		}
		taskID, err := c.PublishValidatedTask(ctx, desc, 2, 0.7, "text-processing")
		if err != nil {
			log.Printf("[publisher] publish task %d: %v", i+1, err)
		} else {
			log.Printf("[publisher] ▶  published %s (validation required, max 2 retries): %.50s...", taskID, desc)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(1500 * time.Millisecond):
		}
	}
	log.Println("[publisher] all tasks published")
}

// runWorker claims tasks with cap "text-processing".
// For tasks that require validation it calls SubmitForValidation; otherwise CompleteTask.
func runWorker(ctx context.Context, agentID, addr string) {
	c := client.NewMailboxClient(agentID, addr)
	for {
		if err := c.Register(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	_ = c.SetRole(ctx, "worker")
	_ = c.SetCapabilities(ctx, "text-processing")
	_ = c.SetStatus(ctx, "idle", "")
	log.Printf("[%s] online  caps=[text-processing]", agentID)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] stopping", agentID)
			return
		default:
		}

		task, err := c.ClaimTask(ctx)
		if err != nil || task == nil {
			sleep(ctx, 400*time.Millisecond)
			continue
		}

		log.Printf("[%s] ✓ claimed %s: %.50s...", agentID, task.ID, task.Description)

		// Simulate processing (includes reading any retry feedback in the description).
		result := simulateWork(ctx, agentID, task.ID, task.Description)
		if ctx.Err() != nil {
			return
		}

		if task.ValidationRequired {
			validateTaskID, err := c.SubmitForValidation(ctx, task.ID, result)
			if err != nil {
				log.Printf("[%s] SubmitForValidation %s: %v", agentID, task.ID, err)
			} else {
				log.Printf("[%s] ↪  submitted %s for validation → %s", agentID, task.ID, validateTaskID)
			}
			// Status is reset to idle by the Hub automatically.
		} else {
			if err := c.CompleteTask(ctx, task.ID, result); err != nil {
				log.Printf("[%s] CompleteTask %s: %v", agentID, task.ID, err)
			}
			_ = c.SetStatus(ctx, "idle", "")
		}
	}
}

// runValidator claims validate tasks (cap "validate") and scores them.
// Scoring is simulated:
//   - First attempt (no "[Retry" in description): 50 % chance of failure (score 0.45)
//   - Retry attempt: 85 % chance of passing (score 0.88)
func runValidator(ctx context.Context, agentID, addr string) {
	c := client.NewMailboxClient(agentID, addr)
	for {
		if err := c.Register(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	_ = c.SetRole(ctx, "validator")
	_ = c.SetCapabilities(ctx, "validate")
	_ = c.SetStatus(ctx, "idle", "")
	log.Printf("[%s] online  caps=[validate]", agentID)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] stopping", agentID)
			return
		default:
		}

		task, err := c.ClaimTask(ctx)
		if err != nil || task == nil {
			sleep(ctx, 400*time.Millisecond)
			continue
		}

		log.Printf("[%s] ✓ claimed validate task %s", agentID, task.ID)
		sleep(ctx, 600*time.Millisecond) // simulate review time

		isRetry := strings.Contains(task.Description, "[Retry")
		var score float64
		var feedback string

		if isRetry {
			// Retry attempts are more likely to pass.
			if rng.Float64() < 0.85 {
				score = 0.80 + rng.Float64()*0.20 // 0.80-1.00
				feedback = "Improved — result is now clear and accurate."
			} else {
				score = 0.40 + rng.Float64()*0.25 // 0.40-0.65
				feedback = "Still lacks depth; please provide concrete examples."
			}
		} else {
			// First attempt: 50 % fail rate to demonstrate retries.
			if rng.Float64() < 0.50 {
				score = 0.35 + rng.Float64()*0.25 // 0.35-0.60
				feedback = failureFeedback(rng)
			} else {
				score = 0.75 + rng.Float64()*0.25 // 0.75-1.00
				feedback = "Good — result is accurate and well-structured."
			}
		}

		if ctx.Err() != nil {
			return
		}

		if err := c.SubmitValidation(ctx, task.ID, score, feedback); err != nil {
			log.Printf("[%s] SubmitValidation %s: %v", agentID, task.ID, err)
		} else {
			symbol := "✔"
			verdict := "PASSED"
			if score < 0.7 {
				symbol = "✗"
				verdict = "FAILED"
			}
			log.Printf("[%s] %s score=%.2f (%s) %s → %q", agentID, symbol, score, verdict, task.ID, feedback)
		}
		_ = c.SetStatus(ctx, "idle", "")
	}
}

// simulateWork mimics an LLM producing a result. If the description contains retry
// feedback the worker "reads" it and produces a better response.
func simulateWork(ctx context.Context, agentID, taskID, description string) string {
	delay := time.Duration(700+len(taskID)*100) * time.Millisecond
	if delay > 1500*time.Millisecond {
		delay = 1500 * time.Millisecond
	}
	sleep(ctx, delay)
	if strings.Contains(description, "[Retry") {
		return fmt.Sprintf("[%s — improved after feedback] %s", agentID, description)
	}
	return fmt.Sprintf("[%s] %s", agentID, description)
}

func failureFeedback(rng *rand.Rand) string {
	reasons := []string{
		"Result is too vague; please be more specific.",
		"Missing key details — expand the explanation.",
		"The tone is informal; rewrite in a professional style.",
		"Incorrect — the answer does not address the question.",
	}
	return reasons[rng.Intn(len(reasons))]
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
