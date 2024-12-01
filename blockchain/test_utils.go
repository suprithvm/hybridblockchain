package blockchain

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

type TestLogger struct {
	mu     sync.Mutex
	file   *os.File
	buffer []string
}

func NewTestLogger(testName string) (*TestLogger, error) {
	file, err := os.OpenFile("test_logs.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open test_logs.txt: %v", err)
	}

	// Write test run header
	header := fmt.Sprintf("\n=== RUN   %s\n", testName)
	if _, err := file.WriteString(header); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to write header: %v", err)
	}

	return &TestLogger{
		file:   file,
		buffer: make([]string, 0),
	}, nil
}

func (l *TestLogger) Log(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	msg := fmt.Sprintf(format, args...)
	l.buffer = append(l.buffer, msg)

	// Get file and line number where the log was called
	_, file, line, _ := runtime.Caller(1)
	logEntry := fmt.Sprintf("    %s:%d: %s\n", file, line, msg)

	_, err := l.file.WriteString(logEntry)
	if err != nil {
		fmt.Printf("Error writing to log file: %v\n", err)
	}
}

func (l *TestLogger) LogError(err error, testName string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	_, file, line, _ := runtime.Caller(1)
	errorLog := fmt.Sprintf(`    %s:%d: 
                Error Trace:    %s:%d
                Error:          %v
                Test:           %s
`, file, line, file, line, err, testName)

	_, err = l.file.WriteString(errorLog)
	if err != nil {
		fmt.Printf("Error writing to log file: %v\n", err)
	}
}

func (l *TestLogger) LogTestResult(testName string, passed bool, duration time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	result := "PASS"
	if !passed {
		result = "FAIL"
	}

	resultLog := fmt.Sprintf("--- %s: %s (%.2fs)\n", result, testName, duration.Seconds())
	_, err := l.file.WriteString(resultLog)
	if err != nil {
		fmt.Printf("Error writing to log file: %v\n", err)
	}
}

func (l *TestLogger) LogAssert(t *testing.T, assertion bool, msg string) {
	if !assertion {
		_, file, line, _ := runtime.Caller(1)
		l.Log("Assertion failed at %s:%d: %s", file, line, msg)
	}
}

func (l *TestLogger) Close() error {
	return l.file.Close()
}
