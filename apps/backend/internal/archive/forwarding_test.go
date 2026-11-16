package archive

import (
	"context"
	"crypto/ecdsa"
	"regexp"
	"sync"
	"testing"
)

func TestForwardingTokenEntropyAndAddress(t *testing.T) {
	const count = 1000
	pattern := regexp.MustCompile(`^[a-z2-7]{16}$`)
	seen := make(map[string]bool, count)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := forwardingToken()
			if err != nil {
				t.Error(err)
				return
			}
			if !pattern.MatchString(token) {
				t.Errorf("invalid token format: %q", token)
			}
			mu.Lock()
			if seen[token] {
				t.Errorf("duplicate random token: %q", token)
			}
			seen[token] = true
			mu.Unlock()
			address := forwardingAddress(token, true)
			if !address.Enabled || address.Address != token+"@inbound.invoyis.com" {
				t.Errorf("invalid address: %+v", address)
			}
		}()
	}
	wg.Wait()
}

func TestForwardingConcurrentLifecycle(t *testing.T) {
	s, _, owner, admin := setup(t)
	s.Storage = &fakeOriginals{objects: map[string][]byte{}}
	// This test concerns address transactions; an arbitrary non-nil key is enough.
	s.InboundPublicKey = &ecdsa.PublicKey{}
	archive, err := s.Create(context.Background(), admin, "personal", "Concurrent forwarding", "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	addresses := make(chan string, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			address, e := s.ChangeForwarding(context.Background(), admin, archive.ID, "enable")
			if e != nil {
				t.Error(e)
				return
			}
			addresses <- address.Address
		}()
	}
	wg.Wait()
	close(addresses)
	first := ""
	for address := range addresses {
		if first == "" {
			first = address
		}
		if address != first {
			t.Fatalf("concurrent enables diverged: %s and %s", first, address)
		}
	}
	if first == "" {
		t.Fatal("no address created")
	}
	disabled, err := s.ChangeForwarding(context.Background(), admin, archive.ID, "disable")
	if err != nil || disabled.Enabled || disabled.Address != first {
		t.Fatalf("disable: %+v %v", disabled, err)
	}
	reenabled, err := s.ChangeForwarding(context.Background(), admin, archive.ID, "enable")
	if err != nil || !reenabled.Enabled || reenabled.Address != first {
		t.Fatalf("re-enable: %+v %v", reenabled, err)
	}
	rotated, err := s.ChangeForwarding(context.Background(), admin, archive.ID, "rotate")
	if err != nil || rotated.Address == first {
		t.Fatalf("rotate: %+v %v", rotated, err)
	}
	var retired, current int
	if err = owner.QueryRow(`SELECT count(*) FILTER (WHERE state='retired'),count(*) FILTER (WHERE state='active') FROM invoyis.forwarding_addresses WHERE archive_id=$1`, archive.ID).Scan(&retired, &current); err != nil || retired != 1 || current != 1 {
		t.Fatalf("address history: %d retired %d active %v", retired, current, err)
	}
	rotations := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, e := s.ChangeForwarding(context.Background(), admin, archive.ID, "rotate")
			if e != nil {
				t.Error(e)
				return
			}
			rotations <- next.Address
		}()
	}
	wg.Wait()
	close(rotations)
	unique := map[string]bool{first: true, rotated.Address: true}
	for next := range rotations {
		if unique[next] {
			t.Fatalf("rotation reused address %s", next)
		}
		unique[next] = true
	}
	if len(unique) != 10 {
		t.Fatalf("only %d unique addresses", len(unique))
	}
	if err = owner.QueryRow(`SELECT count(*) FILTER (WHERE state='retired'),count(*) FILTER (WHERE state='active') FROM invoyis.forwarding_addresses WHERE archive_id=$1`, archive.ID).Scan(&retired, &current); err != nil || retired != 9 || current != 1 {
		t.Fatalf("concurrent rotation history: %d retired %d active %v", retired, current, err)
	}
}
