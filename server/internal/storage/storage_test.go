package storage

import "testing"

func TestOpenDoesNotRequireLiveDependencies(t *testing.T) {
	connections, err := Open("remi:remi@tcp(127.0.0.1:1)/remi", "127.0.0.1:1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := connections.Close(); err != nil {
		t.Fatal(err)
	}
}
