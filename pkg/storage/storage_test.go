package storage

import (
	"os"
	"testing"
)

func TestStorage_LocalDriver(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	driver, err := NewLocalDriver(tmpDir, "http://localhost/storage")
	if err != nil {
		t.Fatal(err)
	}

	content := []byte("Hello GoLaravel Storage")
	err = driver.Put("invoices/inv_1.txt", content)
	if err != nil {
		t.Fatalf("unexpected put error: %v", err)
	}

	if !driver.Exists("invoices/inv_1.txt") {
		t.Fatal("expected file to exist")
	}

	data, err := driver.Get("invoices/inv_1.txt")
	if err != nil || string(data) != string(content) {
		t.Fatalf("unexpected read: %s", string(data))
	}

	_ = driver.Delete("invoices/inv_1.txt")
	if driver.Exists("invoices/inv_1.txt") {
		t.Fatal("expected file to be deleted")
	}
}
