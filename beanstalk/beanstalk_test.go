package beanstalk

import (
	"bytes"
	//"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

type ReadWriter struct {
	io.Reader
	io.Writer
}

func responder(reply string) (io.ReadWriter, *bytes.Buffer) {
	wr := new(bytes.Buffer)
	rd := strings.NewReader(reply)
	return &ReadWriter{rd, wr}, wr
}

func TestPutReplyEOF(t *testing.T) {
	rw, _ := responder("INSERTED 1") // no traling LF, so we hit EOF
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if id != 0 {
		t.Error("expected id 0, got", id)
	}

	if err == nil {
		t.Fatal("expected error, got none")
	}

	berr, ok := err.(Error)

	if !ok {
		t.Fatalf("expected beanstalk.Error, got %T", err)
	}

	if berr.Cmd != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", berr.Cmd)
	}

	if berr.Reply != "INSERTED 1" {
		t.Errorf("reply was %q", berr.Reply)
	}

	if berr.Error != os.EOF {
		t.Errorf("expected os.EOF, got %v", berr.Error)
	}
}

func TestPutReplyUnknown(t *testing.T) {
	rw, _ := responder("FOO 1\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if id != 0 {
		t.Error("expected id 0, got", id)
	}

	if err == nil {
		t.Fatal("expected error, got none")
	}

	berr, ok := err.(Error)

	if !ok {
		t.Fatalf("expected beanstalk.Error, got %T", err)
	}

	if berr.Cmd != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", berr.Cmd)
	}

	if berr.Reply != "FOO 1\n" {
		t.Errorf("reply was %q", berr.Reply)
	}

	if berr.Error != BadReply {
		t.Errorf("expected beanstalk.BadReply, got %v", berr.Error)
	}
}

func TestPutReplyTooManyArgs(t *testing.T) {
	rw, _ := responder("INSERTED 1 2\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if id != 0 {
		t.Error("expected id 0, got", id)
	}

	if err == nil {
		t.Fatal("expected error, got none")
	}

	berr, ok := err.(Error)

	if !ok {
		t.Fatalf("expected beanstalk.Error, got %T", err)
	}

	if berr.Cmd != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", berr.Cmd)
	}

	if berr.Reply != "INSERTED 1 2\n" {
		t.Errorf("reply was %q", berr.Reply)
	}

	if berr.Error != BadReply {
		t.Fatalf("expected beanstalk.BadReply, got %v", berr.Error)
	}
}

func TestPutReplyBadInteger(t *testing.T) {
	rw, _ := responder("INSERTED x\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if id != 0 {
		t.Error("expected id 0, got", id)
	}

	if err == nil {
		t.Fatal("expected error, got none")
	}

	berr, ok := err.(Error)

	if !ok {
		t.Fatalf("expected beanstalk.Error, got %T", err)
	}

	if berr.Cmd != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", berr.Cmd)
	}

	if berr.Reply != "INSERTED x\n" {
		t.Errorf("reply was %q", berr.Reply)
	}

	if berr.Error != BadReply {
		t.Fatalf("expected beanstalk.BadReply, got %v", berr.Error)
	}
}

func TestPutReplyInternalError(t *testing.T) {
	rw, _ := responder("INTERNAL_ERROR\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if id != 0 {
		t.Error("expected id 0, got", id)
	}

	if err == nil {
		t.Fatal("expected error, got none")
	}

	berr, ok := err.(Error)

	if !ok {
		t.Fatalf("expected beanstalk.Error, got %T", err)
	}

	if berr.Cmd != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", berr.Cmd)
	}

	if berr.Reply != "INTERNAL_ERROR\n" {
		t.Errorf("reply was %q", berr.Reply)
	}

	if berr.Error != InternalError {
		t.Fatalf("expected beanstalk.InternalError, got %v", berr.Error)
	}
}

func TestStripTab(t *testing.T) {
	rw, buf := responder("INSERTED 1\t\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if err != nil {
		t.Error("got unexpected error:\n  ", err)
	}

	if id != 1 {
		t.Error("expected id 1, got", id)
	}

	if buf.String() != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", buf.String())
	}
}

func TestStripCR(t *testing.T) {
	rw, buf := responder("INSERTED 1\r\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if err != nil {
		t.Error("got unexpected error:\n  ", err)
	}

	if id != 1 {
		t.Error("expected id 1, got", id)
	}

	if buf.String() != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", buf.String())
	}
}

func TestPut(t *testing.T) {
	rw, buf := responder("INSERTED 1\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if err != nil {
		t.Error("got unexpected error:\n  ", err)
	}

	if id != 1 {
		t.Error("expected id 1, got", id)
	}

	if buf.String() != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", buf.String())
	}
}

func TestPut2(t *testing.T) {
	rw, buf := responder("INSERTED 2\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("default").Put("a", 0, 0, 0)

	if err != nil {
		t.Error("got unexpected error:\n  ", err)
	}

	if id != 2 {
		t.Error("expected id 2, got", id)
	}

	if buf.String() != "put 0 0 0 1\r\na\r\n" {
		t.Errorf("expected put command, got %q", buf.String())
	}
}

func TestPutOtherTube(t *testing.T) {
	rw, buf := responder("USING foo\nINSERTED 1\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("foo").Put("a", 0, 0, 0)

	if err != nil {
		t.Error("got unexpected error:\n  ", err)
	}

	if id != 1 {
		t.Error("expected id 1, got", id)
	}

	if buf.String() != "use foo\r\nput 0 0 0 1\r\na\r\n" {
		t.Errorf("expected use/put command, got %q", buf.String())
	}
}

func TestPutUseFail(t *testing.T) {
	rw, buf := responder("INTERNAL_ERROR\nINSERTED 1\n")
	c := newConn("<fake>", rw)
	id, err := c.Tube("foo").Put("a", 0, 0, 0)

	if buf.String() != "use foo\r\nput 0 0 0 1\r\na\r\n" {
		t.Errorf("expected use/put command, got %q", buf.String())
	}

	if id != 0 {
		t.Error("expected id 0, got", id)
	}

	if err == nil {
		t.Fatal("expected error, got none")
	}

	berr, ok := err.(Error)

	if !ok {
		t.Fatalf("expected beanstalk.Error, got %T", err)
	}

	if berr.Cmd != "use foo\r\n" {
		t.Errorf("expected use command, got %q", berr.Cmd)
	}

	if berr.Reply != "INTERNAL_ERROR\n" {
		t.Errorf("reply was %q", berr.Reply)
	}

	if berr.Error != InternalError {
		t.Fatalf("expected beanstalk.InternalError, got %v", berr.Error)
	}
}

func TestDelete(t *testing.T) {
	rw, buf := responder("DELETED\n")
	c := newConn("<fake>", rw)
	err := c.delete(1)

	if err != nil {
		t.Error("got unexpected error:\n  ", err)
	}

	if buf.String() != "delete 1\r\n" {
		t.Errorf("expected delete command, got %q", buf.String())
	}
}


func TestDeleteNotFound(t *testing.T) {
	rw, _ := responder("NOT_FOUND\n")
	c := newConn("<fake>", rw)
	err := c.delete(1)

	if err == nil {
		t.Fatal("expected error, got none")
	}

	berr, ok := err.(Error)

	if !ok {
		t.Fatalf("expected beanstalk.Error, got %T", err)
	}

	if berr.Cmd != "delete 1\r\n" {
		t.Errorf("expected delete command, got %q", berr.Cmd)
	}

	if berr.Reply != "NOT_FOUND\n" {
		t.Errorf("reply was %q", berr.Reply)
	}

	if berr.Error != NotFound {
		t.Fatalf("expected beanstalk.NotFound, got %v", berr.Error)
	}
}
