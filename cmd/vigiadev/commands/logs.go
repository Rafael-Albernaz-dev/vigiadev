package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var logsFollow, logsTimestamps bool
var logsTail int

var logsCmd = &cobra.Command{
	Use: "logs [service]", Short: "Print or follow persisted service logs",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		service := "all"
		if len(args) == 1 {
			service = args[0]
		}
		if filepath.Base(service) != service || service == "." || service == ".." {
			return fmt.Errorf("invalid service name %q", service)
		}
		path := filepath.Join(cwd, ".vigiadev", "logs", service+".log")
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return streamLogFile(ctx, path, service, logsTail, logsFollow, logsTimestamps, cmd.OutOrStdout())
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow new log lines")
	logsCmd.Flags().IntVarP(&logsTail, "tail", "n", 100, "Number of historical lines to print")
	logsCmd.Flags().BoolVar(&logsTimestamps, "timestamps", false, "Prefix lines with RFC3339 timestamps")
	rootCmd.AddCommand(logsCmd)
}

func streamLogFile(ctx context.Context, path, service string, tail int, follow, timestamps bool, out io.Writer) error {
	if tail < 0 {
		return fmt.Errorf("tail must be zero or greater")
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no logs found for %s; start a session with 'vigiadev up' first", service)
		}
		return err
	}
	defer file.Close()
	lines := make([]string, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	start := len(lines) - tail
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		if _, err := fmt.Fprintln(out, formatLogRecord(line, timestamps)); err != nil {
			return err
		}
	}
	if !follow {
		return nil
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		line, readErr := reader.ReadString('\n')
		if readErr == nil {
			if _, err := fmt.Fprintln(out, formatLogRecord(strings.TrimRight(line, "\r\n"), timestamps)); err != nil {
				return err
			}
			continue
		}
		if readErr != io.EOF {
			return readErr
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func formatLogRecord(record string, timestamps bool) string {
	if !timestamps {
		return record
	}
	fields := strings.SplitN(record, "\t", 4)
	if len(fields) != 4 {
		return record
	}
	stamp, err := time.Parse(time.RFC3339Nano, fields[0])
	if err != nil {
		return record
	}
	return stamp.Format(time.RFC3339) + " " + fields[1] + " " + fields[3]
}
