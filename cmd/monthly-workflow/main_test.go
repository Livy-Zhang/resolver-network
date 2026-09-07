package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestRunWorkflowExecutesWorkersInOrder(t *testing.T) {
	var got []string
	err := runWorkflow("202608", func(name, month string) error {
		got = append(got, name+":"+month)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"settlement-worker:202608", "merkle-worker:202608", "root-worker:202608"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRunWorkflowStopsAfterSettlementFailure(t *testing.T) {
	wantErr := errors.New("settlement unavailable")
	var got []string
	err := runWorkflow("202608", func(name, month string) error {
		got = append(got, name+":"+month)
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(got, []string{"settlement-worker:202608"}) {
		t.Fatalf("got %v", got)
	}
}

func TestRunWorkflowStopsBeforeRootWhenMerkleFails(t *testing.T) {
	wantErr := errors.New("merkle failed")
	var got []string
	err := runWorkflow("202608", func(name, month string) error {
		got = append(got, name+":"+month)
		if name == "merkle-worker" {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	want := []string{"settlement-worker:202608", "merkle-worker:202608"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
