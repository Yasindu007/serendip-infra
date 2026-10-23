package kubernetes

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClient_GetRawKeepsTopLevelFields(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"items":[{"reason":"BackOff","involvedObject":{"name":"p"}}]}`))
	})
	raw, err := client.GetRaw(context.Background(), "/api/v1/namespaces/n/events")
	if err != nil || !strings.Contains(string(raw), `"reason":"BackOff"`) {
		t.Errorf("GetRaw = %s, %v", raw, err)
	}
	if _, err := client.GetRaw(context.Background(), "/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRaw on a 404 = %v", err)
	}
}

func TestClient_GetRawRefusesAnOversizedBody(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", maxRawBody+1)))
	})
	if _, err := client.GetRaw(context.Background(), "/big"); err == nil {
		t.Error("an oversized body was accepted")
	}
}

// Stream hands over lines as the server sends them, with no deadline of
// its own, and ends when the caller's context does.
func TestClient_StreamFollowsUntilCancelled(t *testing.T) {
	release := make(chan struct{})
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
			w.Write([]byte("second\n"))
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	body, err := client.Stream(ctx, "/api/v1/namespaces/n/pods/p/log?follow=true")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	lines := bufio.NewScanner(body)
	if !lines.Scan() || lines.Text() != "first" {
		t.Fatalf("first line = %q", lines.Text())
	}
	close(release)
	if !lines.Scan() || lines.Text() != "second" {
		t.Fatalf("second line = %q", lines.Text())
	}
	cancel()
	done := make(chan bool)
	go func() { done <- lines.Scan() }()
	select {
	case more := <-done:
		if more {
			t.Error("the stream went on after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream didn't end on cancel")
	}
}

func TestClient_StreamStatus(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "nope", http.StatusForbidden)
	})
	if _, err := client.Stream(context.Background(), "/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 = %v", err)
	}
	if _, err := client.Stream(context.Background(), "/forbidden"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("403 = %v", err)
	}
}
