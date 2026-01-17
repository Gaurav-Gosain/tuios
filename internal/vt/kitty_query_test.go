package vt

import (
	"io"
	"testing"
	"time"
)

func TestKittyGraphicsQuery(t *testing.T) {
	e := NewEmulator(80, 24)

	responseChan := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := e.Read(buf)
		if err != nil && err != io.EOF {
			responseChan <- "read error: " + err.Error()
			return
		}
		responseChan <- string(buf[:n])
	}()

	query := "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	_, err := e.Write([]byte(query))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	select {
	case response := <-responseChan:
		expected := "\x1b_Gi=31;OK\x1b\\"
		if response != expected {
			t.Errorf("Expected %q, got %q", expected, response)
		} else {
			t.Logf("Correct response: %q", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestKittyGraphicsQueryNoID(t *testing.T) {
	e := NewEmulator(80, 24)

	responseChan := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := e.Read(buf)
		if err != nil && err != io.EOF {
			responseChan <- "read error: " + err.Error()
			return
		}
		responseChan <- string(buf[:n])
	}()

	query := "\x1b_Ga=q,t=d,f=24;AAAA\x1b\\"
	_, err := e.Write([]byte(query))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	select {
	case response := <-responseChan:
		expected := "\x1b_GOK\x1b\\"
		if response != expected {
			t.Errorf("Expected %q, got %q", expected, response)
		} else {
			t.Logf("Correct response: %q", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}
