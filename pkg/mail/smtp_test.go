package mail

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP answers a minimal SMTP conversation without STARTTLS. When
// silent, it accepts the connection and never speaks.
func fakeSMTP(t *testing.T, silent bool) (int, *strings.Builder) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := &strings.Builder{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if silent {
					time.Sleep(5 * time.Second)
					return
				}
				r := bufio.NewReader(c)
				write := func(s string) { c.Write([]byte(s + "\r\n")) }
				write("220 fake ESMTP")
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if line == ".\r\n" {
							inData = false
							write("250 queued")
							continue
						}
						got.WriteString(line)
						continue
					}
					switch cmd := strings.ToUpper(strings.Fields(line + " x")[0]); cmd {
					case "EHLO", "HELO":
						write("250-fake")
						write("250 8BITMIME")
					case "MAIL", "RCPT", "RSET", "NOOP":
						write("250 ok")
					case "DATA":
						inData = true
						write("354 go")
					case "QUIT":
						write("221 bye")
						return
					default:
						write("502 no")
					}
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func testMsg() *Message {
	return NewMessage().From("app@example.com").To("user@example.com").Subject("Hi").Text("hello")
}

func TestSMTPRequiredStartTLSIsEnforced(t *testing.T) {
	port, got := fakeSMTP(t, false)
	err := NewSMTPDriver(SMTPConfig{Host: "127.0.0.1", Port: port, Encryption: "starttls", Timeout: 2 * time.Second}).Send(testMsg())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("starttls required but not offered: %v", err)
	}
	if got.Len() != 0 {
		t.Fatal("message was sent in plain text despite starttls")
	}
}

func TestSMTPPlainDelivers(t *testing.T) {
	port, got := fakeSMTP(t, false)
	if err := NewSMTPDriver(SMTPConfig{Host: "127.0.0.1", Port: port, Timeout: 2 * time.Second}).Send(testMsg()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(got.String(), "Subject: Hi") {
		t.Fatalf("message not delivered: %q", got.String())
	}
}

func TestSMTPTimeout(t *testing.T) {
	port, _ := fakeSMTP(t, true)
	start := time.Now()
	err := NewSMTPDriver(SMTPConfig{Host: "127.0.0.1", Port: port, Timeout: 300 * time.Millisecond}).Send(testMsg())
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("silent server must time out quickly: %v after %v", err, time.Since(start))
	}
}
