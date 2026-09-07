package main

// run the monthly workflow, triggered by an external scheduler
import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func main() {
	month := time.Now().UTC().AddDate(0, -1, 0).Format("200601")
	if v := os.Getenv("WORKFLOW_MONTH"); v != "" {
		month = v
	}
	if err := runWorkflow(month, runWorker); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type workerRunner func(string, string) error

func runWorker(name, month string) error {
	cmd := exec.Command("/app/"+name, "--month", month)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func runWorkflow(month string, run workerRunner) error {
	for _, name := range []string{"settlement-worker", "merkle-worker", "root-worker"} {
		if err := run(name, month); err != nil {
			return err
		}
	}
	return nil
}
