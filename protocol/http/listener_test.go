package http

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jexia/maestro/codec/json"
	"github.com/jexia/maestro/flow"
	"github.com/jexia/maestro/protocol"
	"github.com/jexia/maestro/refs"
	"github.com/jexia/maestro/specs"
)

func NewMockListener(t *testing.T, nodes flow.Nodes) (protocol.Listener, int) {
	port := AvailablePort(t)
	addr := fmt.Sprintf(":%d", port)
	listener := NewListener(addr, nil)

	req, err := json.NewConstructor().New("input", NewSimpleMockSpecs())
	if err != nil {
		t.Fatal(err)
	}

	res, err := json.NewConstructor().New("output", NewSimpleMockSpecs())
	if err != nil {
		t.Fatal(err)
	}

	endpoints := []*protocol.Endpoint{
		{
			Request: req,
			Flow:    flow.NewManager("test", nodes),
			Options: specs.Options{
				EndpointOption: "/",
				MethodOption:   "POST",
			},
			Response: res,
		},
	}

	listener.Handle(endpoints)
	return listener, port
}

func TestListener(t *testing.T) {
	called := 0
	nodes := flow.Nodes{
		{
			Name:     "first",
			Previous: flow.Nodes{},
			Call: func(ctx context.Context, refs *refs.Store) error {
				called++
				return nil
			},
			Next: flow.Nodes{},
		},
	}

	listener, port := NewMockListener(t, nodes)
	defer listener.Close()
	go listener.Serve()

	// Some CI pipelines take a little while before the listener is active
	time.Sleep(100 * time.Millisecond)

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/", port)
	result, err := http.Post(endpoint, "application/json", strings.NewReader(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}

	if result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code %d", result.StatusCode)
	}

	if called != 1 {
		t.Errorf("unexpected called %d, expected %d", called, len(nodes))
	}
}

func TestListenerBadRequest(t *testing.T) {
	called := 0
	nodes := flow.Nodes{
		{
			Name:     "first",
			Previous: flow.Nodes{},
			Call: func(ctx context.Context, refs *refs.Store) error {
				called++
				return nil
			},
			Next: flow.Nodes{},
		},
	}

	listener, port := NewMockListener(t, nodes)
	defer listener.Close()
	go listener.Serve()

	// Some CI pipelines take a little while before the listener is active
	time.Sleep(100 * time.Millisecond)

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/", port)
	result, err := http.Post(endpoint, "application/json", strings.NewReader(`{"message":}`))
	if err != nil {
		t.Fatal(err)
	}

	if result.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected status code %d, expected %d", result.StatusCode, http.StatusBadRequest)
	}

	if called == 1 {
		t.Errorf("unexpected called %d, expected %d", called, 0)
	}
}

func TestPathReferences(t *testing.T) {
	port := AvailablePort(t)
	addr := fmt.Sprintf(":%d", port)
	listener := NewListener(addr, nil)

	defer listener.Close()

	message := "active"
	nodes := flow.Nodes{
		{
			Name:     "first",
			Previous: flow.Nodes{},
			Call: func(ctx context.Context, refs *refs.Store) error {
				ref := refs.Load("input", "message")
				if ref == nil {
					t.Fatal("input:message ref has not been set")
				}

				if ref.Value != message {
					t.Fatalf("unexpected ref value %+v, expected %+v", ref.Value, message)
				}

				return nil
			},
			Next: flow.Nodes{},
		},
	}

	endpoints := []*protocol.Endpoint{
		{
			Flow: flow.NewManager("test", nodes),
			Options: specs.Options{
				"endpoint": "/:message",
				"method":   "GET",
			},
		},
	}

	listener.Handle(endpoints)
	go listener.Serve()

	// Some CI pipelines take a little while before the listener is active
	time.Sleep(100 * time.Millisecond)

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/"+message, port)
	http.Get(endpoint)
}
