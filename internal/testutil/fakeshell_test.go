package testutil_test

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// =============================================================================
// FakeShell Tests
// =============================================================================

func TestFakeShell_WriteRead(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	// Send output from the "shell"
	shell.SendOutput("Hello from shell\n")

	// Read the output
	buf := make([]byte, 100)
	n, err := shell.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	got := string(buf[:n])
	if got != "Hello from shell\n" {
		t.Errorf("Expected 'Hello from shell\\n', got %q", got)
	}
}

func TestFakeShell_Input(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	// Write input to the shell
	_, err := shell.Write([]byte("ls -la\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Check input was recorded
	got := shell.GetInput()
	if got != "ls -la\n" {
		t.Errorf("Expected 'ls -la\\n', got %q", got)
	}

	// Check input history
	history := shell.GetInputHistory()
	if len(history) != 1 || history[0] != "ls -la\n" {
		t.Errorf("Expected history ['ls -la\\n'], got %v", history)
	}
}

func TestFakeShell_ClearInput(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	_, _ = shell.Write([]byte("test"))
	shell.ClearInput()

	if shell.GetInput() != "" {
		t.Error("Expected empty input after clear")
	}
	if len(shell.GetInputHistory()) != 0 {
		t.Error("Expected empty history after clear")
	}
}

func TestFakeShell_Close(t *testing.T) {
	shell := testutil.NewFakeShell()

	// Close the shell
	err := shell.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Subsequent writes should fail
	_, err = shell.Write([]byte("test"))
	if err == nil {
		t.Error("Expected error when writing to closed shell")
	}
}

func TestFakeShell_DoubleClose(t *testing.T) {
	shell := testutil.NewFakeShell()

	// First close should succeed
	if err := shell.Close(); err != nil {
		t.Fatalf("First close failed: %v", err)
	}

	// Second close should not panic
	if err := shell.Close(); err != nil {
		t.Fatalf("Second close failed: %v", err)
	}
}

func TestFakeShell_ReadAfterClose(t *testing.T) {
	shell := testutil.NewFakeShell()
	_ = shell.Close()

	buf := make([]byte, 100)
	n, err := shell.Read(buf)
	if err != io.EOF {
		t.Errorf("Expected io.EOF, got %v", err)
	}
	if n != 0 {
		t.Errorf("Expected 0 bytes, got %d", n)
	}
}

func TestFakeShell_SendOutputAfterClose(t *testing.T) {
	shell := testutil.NewFakeShell()
	_ = shell.Close()

	// Should not panic
	shell.SendOutput("should be ignored")
}

func TestFakeShell_IsClosed(t *testing.T) {
	shell := testutil.NewFakeShell()

	if shell.IsClosed() {
		t.Error("Expected shell to not be closed initially")
	}

	_ = shell.Close()

	if !shell.IsClosed() {
		t.Error("Expected shell to be closed after Close()")
	}
}

func TestFakeShell_SendOutputf(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	shell.SendOutputf("Hello %s, you are %d years old\n", "Alice", 30)

	buf := make([]byte, 200)
	n, err := shell.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	got := string(buf[:n])
	expected := "Hello Alice, you are 30 years old\n"
	if got != expected {
		t.Errorf("Expected %q, got %q", expected, got)
	}
}

func TestFakeShell_ReadWithTimeout(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	buf := make([]byte, 100)
	_, err := shell.ReadWithTimeout(buf, 50*time.Millisecond)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("Expected timeout error, got %v", err)
	}
}

func TestFakeShell_ReadWithTimeout_Data(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	go func() {
		time.Sleep(10 * time.Millisecond)
		shell.SendOutput("hello\n")
	}()

	buf := make([]byte, 100)
	n, err := shell.ReadWithTimeout(buf, time.Second)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	got := string(buf[:n])
	if got != "hello\n" {
		t.Errorf("Expected %q, got %q", "hello\n", got)
	}
}

func TestFakeShell_LargeOutput(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	// Send 1MB+ of data
	data := strings.Repeat("A", 1024*1024+1)
	shell.SendOutput(data)

	// Read it all back
	var total int
	buf := make([]byte, 32*1024)
	for total < len(data) {
		n, err := shell.Read(buf)
		if err != nil {
			t.Fatalf("Read failed at %d bytes: %v", total, err)
		}
		total += n
	}

	if total != len(data) {
		t.Errorf("Expected %d bytes, got %d", len(data), total)
	}
}

func TestFakeShell_PartialRead(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	shell.SendOutput("Hello World")

	// Read with a small buffer
	buf := make([]byte, 5)
	n, err := shell.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 5 {
		t.Errorf("Expected 5 bytes, got %d", n)
	}
	if string(buf[:n]) != "Hello" {
		t.Errorf("Expected 'Hello', got %q", string(buf[:n]))
	}

	// Read the rest
	buf2 := make([]byte, 20)
	n2, err := shell.Read(buf2)
	if err != nil {
		t.Fatalf("Second read failed: %v", err)
	}
	if string(buf2[:n2]) != " World" {
		t.Errorf("Expected ' World', got %q", string(buf2[:n2]))
	}
}

func TestFakeShell_MultipleWrites(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	_, _ = shell.Write([]byte("first "))
	_, _ = shell.Write([]byte("second "))
	_, _ = shell.Write([]byte("third"))

	got := shell.GetInput()
	if got != "first second third" {
		t.Errorf("Expected 'first second third', got %q", got)
	}

	history := shell.GetInputHistory()
	if len(history) != 3 {
		t.Fatalf("Expected 3 history entries, got %d", len(history))
	}
	if history[0] != "first " || history[1] != "second " || history[2] != "third" {
		t.Errorf("Unexpected history: %v", history)
	}
}

func TestFakeShell_ReadCopyOverflow(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	// Send data larger than read buffer
	shell.SendOutput("ABCDEFGHIJ") // 10 bytes

	buf := make([]byte, 4) // only 4 bytes
	n, err := shell.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// Should only read up to buffer size
	if n > len(buf) {
		t.Errorf("Read returned %d bytes, exceeding buffer size %d", n, len(buf))
	}
}

func TestFakeShell_ConcurrentClose(t *testing.T) {
	shell := testutil.NewFakeShell()

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = shell.Close()
		}()
	}

	wg.Wait()
	// Should not panic
}

func TestErrorOutput(t *testing.T) {
	got := testutil.ErrorOutput("ls", "No such file or directory")
	expected := "bash: ls: No such file or directory\n"
	if got != expected {
		t.Errorf("Expected %q, got %q", expected, got)
	}
}

func TestCommandNotFound(t *testing.T) {
	got := testutil.CommandNotFound("foo")
	expected := "bash: foo: command not found\n"
	if got != expected {
		t.Errorf("Expected %q, got %q", expected, got)
	}
}

func TestTabCompletionResponse(t *testing.T) {
	got := testutil.TabCompletionResponse([]string{"file1.txt", "file2.txt", "dir/"})
	expected := "file1.txt  file2.txt  dir/\r\n"
	if got != expected {
		t.Errorf("Expected %q, got %q", expected, got)
	}
}

// =============================================================================
// ANSIBuilder Tests
// =============================================================================

func TestANSIBuilder_Text(t *testing.T) {
	b := testutil.NewANSIBuilder()
	result := b.Text("Hello").Text(" ").Text("World").String()

	if result != "Hello World" {
		t.Errorf("Expected 'Hello World', got %q", result)
	}
}

func TestANSIBuilder_CursorMovement(t *testing.T) {
	tests := []struct {
		name     string
		build    func(*testutil.ANSIBuilder) string
		expected string
	}{
		{
			name: "cursor home",
			build: func(b *testutil.ANSIBuilder) string {
				return b.CursorHome().String()
			},
			expected: "\x1b[H",
		},
		{
			name: "cursor to position",
			build: func(b *testutil.ANSIBuilder) string {
				return b.CursorTo(10, 20).String()
			},
			expected: "\x1b[10;20H",
		},
		{
			name: "cursor up",
			build: func(b *testutil.ANSIBuilder) string {
				return b.CursorUp(5).String()
			},
			expected: "\x1b[5A",
		},
		{
			name: "cursor down single",
			build: func(b *testutil.ANSIBuilder) string {
				return b.CursorDown(1).String()
			},
			expected: "\x1b[B",
		},
		{
			name: "cursor forward",
			build: func(b *testutil.ANSIBuilder) string {
				return b.CursorForward(3).String()
			},
			expected: "\x1b[3C",
		},
		{
			name: "cursor backward single",
			build: func(b *testutil.ANSIBuilder) string {
				return b.CursorBackward(1).String()
			},
			expected: "\x1b[D",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.build(testutil.NewANSIBuilder())
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestANSIBuilder_ScreenControl(t *testing.T) {
	tests := []struct {
		name     string
		build    func(*testutil.ANSIBuilder) string
		expected string
	}{
		{
			name: "clear screen",
			build: func(b *testutil.ANSIBuilder) string {
				return b.ClearScreen().String()
			},
			expected: "\x1b[2J",
		},
		{
			name: "clear line",
			build: func(b *testutil.ANSIBuilder) string {
				return b.ClearLine().String()
			},
			expected: "\x1b[2K",
		},
		{
			name: "clear to end of line",
			build: func(b *testutil.ANSIBuilder) string {
				return b.ClearToEndOfLine().String()
			},
			expected: "\x1b[K",
		},
		{
			name: "clear to end of screen",
			build: func(b *testutil.ANSIBuilder) string {
				return b.ClearToEndOfScreen().String()
			},
			expected: "\x1b[J",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.build(testutil.NewANSIBuilder())
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestANSIBuilder_SGR(t *testing.T) {
	tests := []struct {
		name     string
		build    func(*testutil.ANSIBuilder) string
		expected string
	}{
		{
			name: "reset",
			build: func(b *testutil.ANSIBuilder) string {
				return b.Reset().String()
			},
			expected: "\x1b[0m",
		},
		{
			name: "bold",
			build: func(b *testutil.ANSIBuilder) string {
				return b.Bold().String()
			},
			expected: "\x1b[1m",
		},
		{
			name: "italic",
			build: func(b *testutil.ANSIBuilder) string {
				return b.Italic().String()
			},
			expected: "\x1b[3m",
		},
		{
			name: "underline",
			build: func(b *testutil.ANSIBuilder) string {
				return b.Underline().String()
			},
			expected: "\x1b[4m",
		},
		{
			name: "fg color",
			build: func(b *testutil.ANSIBuilder) string {
				return b.FgColor(31).String()
			},
			expected: "\x1b[31m",
		},
		{
			name: "bg color",
			build: func(b *testutil.ANSIBuilder) string {
				return b.BgColor(44).String()
			},
			expected: "\x1b[44m",
		},
		{
			name: "256 color fg",
			build: func(b *testutil.ANSIBuilder) string {
				return b.Fg256(196).String()
			},
			expected: "\x1b[38;5;196m",
		},
		{
			name: "256 color bg",
			build: func(b *testutil.ANSIBuilder) string {
				return b.Bg256(200).String()
			},
			expected: "\x1b[48;5;200m",
		},
		{
			name: "rgb fg",
			build: func(b *testutil.ANSIBuilder) string {
				return b.FgRGB(255, 128, 0).String()
			},
			expected: "\x1b[38;2;255;128;0m",
		},
		{
			name: "rgb bg",
			build: func(b *testutil.ANSIBuilder) string {
				return b.BgRGB(0, 128, 255).String()
			},
			expected: "\x1b[48;2;0;128;255m",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.build(testutil.NewANSIBuilder())
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestANSIBuilder_Modes(t *testing.T) {
	tests := []struct {
		name     string
		build    func(*testutil.ANSIBuilder) string
		expected string
	}{
		{
			name: "alt screen",
			build: func(b *testutil.ANSIBuilder) string {
				return b.AltScreen().String()
			},
			expected: "\x1b[?1049h",
		},
		{
			name: "main screen",
			build: func(b *testutil.ANSIBuilder) string {
				return b.MainScreen().String()
			},
			expected: "\x1b[?1049l",
		},
		{
			name: "show cursor",
			build: func(b *testutil.ANSIBuilder) string {
				return b.ShowCursor().String()
			},
			expected: "\x1b[?25h",
		},
		{
			name: "hide cursor",
			build: func(b *testutil.ANSIBuilder) string {
				return b.HideCursor().String()
			},
			expected: "\x1b[?25l",
		},
		{
			name: "enable bracketed paste",
			build: func(b *testutil.ANSIBuilder) string {
				return b.EnableBracketedPaste().String()
			},
			expected: "\x1b[?2004h",
		},
		{
			name: "disable bracketed paste",
			build: func(b *testutil.ANSIBuilder) string {
				return b.DisableBracketedPaste().String()
			},
			expected: "\x1b[?2004l",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.build(testutil.NewANSIBuilder())
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestANSIBuilder_OSC(t *testing.T) {
	b := testutil.NewANSIBuilder()
	result := b.OSCTitle("My Terminal").String()

	expected := "\x1b]0;My Terminal\x07"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestANSIBuilder_Scroll(t *testing.T) {
	tests := []struct {
		name     string
		build    func(*testutil.ANSIBuilder) string
		expected string
	}{
		{
			name: "scroll region",
			build: func(b *testutil.ANSIBuilder) string {
				return b.ScrollRegion(5, 20).String()
			},
			expected: "\x1b[5;20r",
		},
		{
			name: "scroll up",
			build: func(b *testutil.ANSIBuilder) string {
				return b.ScrollUp(3).String()
			},
			expected: "\x1b[3S",
		},
		{
			name: "scroll down single",
			build: func(b *testutil.ANSIBuilder) string {
				return b.ScrollDown(1).String()
			},
			expected: "\x1b[T",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.build(testutil.NewANSIBuilder())
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestANSIBuilder_Chaining(t *testing.T) {
	b := testutil.NewANSIBuilder()
	result := b.
		ClearScreen().
		CursorHome().
		Bold().
		FgColor(32).
		Text("Hello").
		Reset().
		String()

	// Should contain all parts
	if !strings.Contains(result, "\x1b[2J") {
		t.Error("Missing clear screen")
	}
	if !strings.Contains(result, "\x1b[H") {
		t.Error("Missing cursor home")
	}
	if !strings.Contains(result, "\x1b[1m") {
		t.Error("Missing bold")
	}
	if !strings.Contains(result, "\x1b[32m") {
		t.Error("Missing fg color")
	}
	if !strings.Contains(result, "Hello") {
		t.Error("Missing text")
	}
	if !strings.Contains(result, "\x1b[0m") {
		t.Error("Missing reset")
	}
}

func TestANSIBuilder_Clear(t *testing.T) {
	b := testutil.NewANSIBuilder()
	b.Text("First")
	b.Clear()
	b.Text("Second")

	result := b.String()
	if result != "Second" {
		t.Errorf("Expected 'Second' after clear, got %q", result)
	}
}

func TestANSIBuilder_Bytes(t *testing.T) {
	b := testutil.NewANSIBuilder()
	b.Text("Test")

	bytes := b.Bytes()
	if string(bytes) != "Test" {
		t.Errorf("Expected 'Test', got %q", string(bytes))
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestShellPrompt(t *testing.T) {
	prompt := testutil.ShellPrompt("user", "host", "~")

	if !strings.Contains(prompt, "user@host") {
		t.Errorf("Expected 'user@host' in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "~") {
		t.Errorf("Expected '~' in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "$ ") {
		t.Errorf("Expected '$ ' in prompt, got %q", prompt)
	}
}

func TestColoredLine(t *testing.T) {
	line := testutil.ColoredLine(31, "Red text")

	if !strings.Contains(line, "Red text") {
		t.Errorf("Expected 'Red text' in line, got %q", line)
	}
	if !strings.Contains(line, "\x1b[31m") {
		t.Errorf("Expected red color code in line, got %q", line)
	}
	if !strings.Contains(line, "\r\n") {
		t.Errorf("Expected newline in line, got %q", line)
	}
}

func TestLSOutput(t *testing.T) {
	output := testutil.LSOutput(
		[]string{"dir1", "file.txt", "dir2"},
		[]bool{true, false, true},
	)

	if !strings.Contains(output, "dir1") {
		t.Error("Expected 'dir1' in output")
	}
	if !strings.Contains(output, "file.txt") {
		t.Error("Expected 'file.txt' in output")
	}
	// Directories should be colored (bold blue)
	if !strings.Contains(output, "\x1b[34m") {
		t.Error("Expected blue color for directories")
	}
}

func TestProgressBar(t *testing.T) {
	bar0 := testutil.ProgressBar(0, 20)
	if !strings.Contains(bar0, "0%") {
		t.Errorf("Expected 0%% in progress bar, got %q", bar0)
	}

	bar50 := testutil.ProgressBar(50, 20)
	if !strings.Contains(bar50, "50%") {
		t.Errorf("Expected 50%% in progress bar, got %q", bar50)
	}

	bar100 := testutil.ProgressBar(100, 20)
	if !strings.Contains(bar100, "100%") {
		t.Errorf("Expected 100%% in progress bar, got %q", bar100)
	}
}

func TestSpinnerFrame(t *testing.T) {
	frames := []string{"|", "/", "-", "\\"}
	for i := range 8 {
		frame := testutil.SpinnerFrame(i)
		expected := frames[i%4]
		if !strings.Contains(frame, expected) {
			t.Errorf("Frame %d: expected %q, got %q", i, expected, frame)
		}
	}
}

func TestCursorPositionResponse(t *testing.T) {
	response := testutil.CursorPositionResponse(10, 20)
	expected := "\x1b[10;20R"
	if response != expected {
		t.Errorf("Expected %q, got %q", expected, response)
	}
}

func TestTerminalSizeResponse(t *testing.T) {
	response := testutil.TerminalSizeResponse(24, 80)
	expected := "\x1b[8;24;80t"
	if response != expected {
		t.Errorf("Expected %q, got %q", expected, response)
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestFakeShell_Concurrent(t *testing.T) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	// Send output in a goroutine
	go func() {
		for range 10 {
			shell.SendOutput("line\n")
			time.Sleep(time.Millisecond)
		}
	}()

	// Write input in another goroutine
	go func() {
		for range 10 {
			_, _ = shell.Write([]byte("cmd\n"))
			time.Sleep(time.Millisecond)
		}
	}()

	// Read from shell
	buf := make([]byte, 100)
	for range 10 {
		_, _ = shell.Read(buf)
	}

	// Should not panic or deadlock
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkANSIBuilder_Simple(b *testing.B) {
	for b.Loop() {
		builder := testutil.NewANSIBuilder()
		_ = builder.Text("Hello World").String()
	}
}

func BenchmarkANSIBuilder_Complex(b *testing.B) {
	for b.Loop() {
		builder := testutil.NewANSIBuilder()
		_ = builder.
			ClearScreen().
			CursorHome().
			Bold().
			FgRGB(255, 128, 0).
			Text("Styled Text").
			Reset().
			Newline().
			String()
	}
}

func BenchmarkFakeShell_SendOutput(b *testing.B) {
	shell := testutil.NewFakeShell()
	defer func() { _ = shell.Close() }()

	for b.Loop() {
		shell.SendOutput("test output\n")
	}
}
