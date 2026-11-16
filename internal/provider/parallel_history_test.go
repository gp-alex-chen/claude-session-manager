package provider

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
)

type parallelHistoryProvider struct {
	id   ID
	read func(context.Context) ([]Session, error)
}

func (p parallelHistoryProvider) Descriptor() Descriptor {
	return Descriptor{ID: p.id, Name: string(p.id)}
}
func (p parallelHistoryProvider) ValidateSessionID(string) error { return nil }
func (p parallelHistoryProvider) ListSessions(ctx context.Context) ([]Session, error) {
	return p.read(ctx)
}

func (p parallelHistoryProvider) PrepareLaunch(context.Context, OpenRequest) (LaunchSpec, error) {
	return LaunchSpec{}, errors.New("fixture does not launch")
}

func TestRegistryHistoriesRunConcurrentlyWithStableOrderAndPartialFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release, fastStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
		r := NewRegistry()
		r.Register(parallelHistoryProvider{"slow", func(context.Context) ([]Session, error) {
			close(entered)
			<-release
			return []Session{{Ref: SessionRef{"slow", "one"}}}, nil
		}})
		r.Register(parallelHistoryProvider{"fast", func(context.Context) ([]Session, error) {
			close(fastStarted)
			return []Session{{Ref: SessionRef{"fast", "cached"}}}, errors.New("synthetic fast failure")
		}})
		done := make(chan struct{})
		var rows []Session
		var succeeded []ID
		var warnings []error
		go func() { rows, succeeded, warnings = r.QuerySessions(context.Background(), nil); close(done) }()
		<-entered
		synctest.Wait()
		concurrent := false
		select {
		case <-fastStarted:
			concurrent = true
		default:
		}
		close(release)
		<-done
		if !concurrent {
			t.Fatal("slow provider prevented another provider from starting")
		}
		if len(rows) != 2 || rows[0].Ref.Provider != "slow" || rows[1].Ref.Provider != "fast" || !reflect.DeepEqual(succeeded, []ID{"slow"}) || len(warnings) != 1 {
			t.Fatalf("aggregation changed: %v %v %v", rows, succeeded, warnings)
		}
		rows, succeeded, warnings = r.QuerySessions(context.Background(), []ID{})
		if len(rows)+len(succeeded)+len(warnings) != 0 {
			t.Fatal("empty selection queried providers")
		}
	})
}
