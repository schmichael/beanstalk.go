// TODO write package doc.
//
// We are lenient about the protocol -- we accept either CR LF or just LF to
// terminate server replies. We also trim white space around words in reply
// lines.
package beanstalk

import (
	"bufio"
	"container/list"
	"container/vector"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Microseconds
type Usec int64

type Job struct {
	Id uint64
	Body string
}

type Conn struct {
	Name string
	ch chan<- []op
}

type Tube struct {
	Name string
	c Conn
}

type Tubes struct {
	Names []string
	c Conn
}

// Implements os.Error
type Error struct {
	Conn Conn
	Cmd string
	Reply string
	Error os.Error
}

func (e Error) String() string {
	return fmt.Sprintf("%s: %q -> %q: %s", e.Conn.Name, e.Cmd, e.Reply, e.Error.String());
}

// This type implements os.Error.
type replyError int

const (
	badReply replyError = iota
	outOfMemory
	internalError
	draining
	badFormat
	unknownCommand
	buried
	expectedCrLf
	jobTooBig
	deadlineSoon
	timedOut
	notFound
	notIgnored
)

// For timeouts. Actually not infinite; merely large. About 126 years.
const Infinity = Usec(4000000000000000)

// Error responses that can be returned by the server.
var (

	// The server sent a bad reply. For example: unknown or inappropriate
	// response, wrong number of terms, or invalid format.
	BadReply os.Error = badReply

	OutOfMemory os.Error = outOfMemory
	InternalError os.Error = internalError
	Draining os.Error = draining
	BadFormat os.Error = badFormat
	UnknownCommand os.Error = unknownCommand
	Buried os.Error = buried
	ExpectedCrLf os.Error = expectedCrLf
	JobTooBig os.Error = jobTooBig
	DeadlineSoon os.Error = deadlineSoon
	TimedOut os.Error = timedOut
	NotFound os.Error = notFound
	NotIgnored os.Error = notIgnored
)

var errorNames []string = []string {
	"Bad Reply",
	"Out of Memory",
	"Internal Error",
	"Draining",
	"Bad Format",
	"Unknown Command",
	"Buried",
	"Expected CR LF",
	"Job Too Big",
	"Deadline Soon",
	"Timed Out",
	"Not Found",
	"Not Ignored",
}

var replyErrors = map[string]os.Error {
	"INTERNAL_ERROR": InternalError,
	"OUT_OF_MEMORY": OutOfMemory,
}

func (e replyError) String() string {
	return "Server " + errorNames[e]
}

func (x Usec) Milliseconds() int64 {
	return int64(x) / 1000
}

func (x Usec) Seconds() int64 {
	return x.Milliseconds() / 1000
}

type op struct {
	cmd string
	tubes []string // For commands that depend on the watch list.
	promise chan<- result
}

type result struct {
	line string // The unparsed reply line.
	body string // The body, if any.
	name string // The first word of the reply line.
	args []string // The other words of the reply line.
	err os.Error // An error, if any.
}

func append(ops, more []op) []op {
	l := len(ops)
	if l + len(more) > cap(ops) { // need to grow?
		newOps := make([]op, (l + len(more)) * 2) // double
		for i, o := range ops {
			newOps[i] = o
		}
		ops = newOps
	}
	ops = ops[0:l + len(more)] // increase the len, not the cap
	for i, o := range more {
		ops[l + i] = o
	}
	return ops
}

func append1(ops []op, o op) []op {
	return append(ops, []op{o})
}

// Read from toSend as many items as possible without blocking.
func collect(toSend <-chan []op) (ops []op) {
	seq := <-toSend
	ops = append(ops, seq)

	for more := true; more; {
		seq, more = <-toSend
		ops = append(ops, seq)
	}

	return
}

func (o op) resolve(line, body, name string, args []string, err os.Error) {
	go func() {
		o.promise <- result{line, body, name, args, err}
	}()
}

func (o op) resolveErr(line string, err os.Error) {
	o.resolve(line, "", "", []string{}, err)
}

// Optimize ops WRT the used tube.
func optUsed(tube string, ops []op) (string, []op) {
	newOps := make([]op, 0, len(ops))
	for _, o := range ops {
		if o.cmd[0:4] == "use " {
			newTube := strings.TrimSpace(o.cmd[4:])

			// Leave out this command and resolve its promise
			// directly.
			if newTube == tube {
				o.resolve("", "", "", []string{}, nil)
				continue
			}

			tube = newTube
		}
		newOps = newOps[0:len(newOps) + 1]
		newOps[len(newOps) - 1] = o
	}
	return tube, newOps
}

func watchOp(tube string) (o op) {
	o.cmd = fmt.Sprintf("watch %s\r\n", tube)
	o.promise = make(chan result)
	return
}

func ignoreOp(tube string) (o op) {
	o.cmd = fmt.Sprintf("ignore %s\r\n", tube)
	o.promise = make(chan result)
	return
}

// Optimize/generate ops WRT the Watch list.
func optWatched(tubes []string, ops []op) ([]string, []op) {
	tubeMap := make(map[string]bool)
	for _, s := range tubes {
		tubeMap[s] = true
	}
	newOps := make([]op, 0, len(ops))
	for _, o := range ops {
		if strings.HasPrefix(o.cmd, "reserve-with-timeout ") {
			newTubes := o.tubes
			newTubeMap := make(map[string]bool)
			for _, s := range newTubes {
				newTubeMap[s] = true
			}

			for _, s := range newTubes {
				if _, ok := tubeMap[s]; !ok {
					newOps = append1(newOps, watchOp(s))
				}
			}

			for _, s := range tubes {
				if _, ok := newTubeMap[s]; !ok {
					newOps = append1(newOps, ignoreOp(s))
				}
			}

			tubes = newTubes
			tubeMap = newTubeMap
		}
		newOps = append1(newOps, o)
	}
	return tubes, newOps
}

// Reordering, compressing, optimization.
func prepare(ops []op) string {
	var cmds vector.StringVector
	for _, o := range ops {
		cmds.Push(o.cmd)
	}

	return strings.Join([]string(cmds), "")
}

func send(toSend <-chan []op, wr io.Writer, sent chan<- op) {
	used := "default"
	watched := []string{"default"}
	for {
		ops := collect(toSend)
		used, ops = optUsed(used, ops)
		watched, ops = optWatched(watched, ops)
		cmds := prepare(ops)

		n, err := io.WriteString(wr, cmds)

		if err != nil {
			fmt.Printf("got err %s\n", err)
		}

		if n != len(cmds) {
			fmt.Printf("bad len %d != %d\n", n, len(cmds))
		}

		for _, o := range ops {
			sent <- o
		}

	}
}

func bodyLen(reply string, args []string) int {
	switch reply {
	case "FOUND", "RESERVED":
		if len(args) != 2 {
			return 0
		}
		l, err := strconv.Atoi(args[1])
		if err != nil {
			return 0
		}
		return l
	}
	return 0
}

func maps(f func(string) string, ss []string) (out []string) {
	out = make([]string, len(ss))
	for i, s := range ss {
		out[i] = f(s)
	}
	return
}

func resultErr(line string, err os.Error) result {
	return result{line, "", "", []string{}, err}
}

func recv(raw io.Reader, ops <-chan op) {
	rd := bufio.NewReader(raw)
	for {
		// Read the next server reply.
		line, err := rd.ReadString('\n')

		if err != nil {
			(<-ops).resolveErr(line, err)
			return
		}

		split := maps(strings.TrimSpace, strings.Split(line, " ", 0))
		reply, args := split[0], split[1:]

		// Read the body, if any.
		var body []byte
		if n := bodyLen(reply, args); n > 0 {
			body = make([]byte, n)
			r, err := io.ReadFull(rd, body)

			if err != nil {
				panic("2 todo properly teardown the Conn")
			}

			if r != n {
				panic("3 todo properly teardown the Conn")
			}
		}

		// Get the corresponding op and deliver the result.
		(<-ops).resolve(line, string(body), reply, args, nil)
	}
}

func flow(in chan op, out chan op) {
	pipeline := list.New()
	for {
		nextOut := pipeline.Front()
		if nextOut != nil {
			select {
			case nextIn := <-in:
				   pipeline.PushBack(nextIn)
			case out <- nextOut.Value.(op):
				   pipeline.Remove(nextOut)
			}
		} else {
			pipeline.PushBack(<-in)
		}
	}
}

// The name parameter should be descriptive. It is usually the remote address
// of the connection.
func newConn(name string, rw io.ReadWriter) Conn {
	toSend := make(chan []op)
	a, b := make(chan op), make(chan op)

	go send(toSend, rw, a)
	go flow(a, b) // Simulate a buffered channel with unlimited capacity.
	go recv(rw, b)

	return Conn{name, toSend}
}

// Put a job into the queue.
func (c Conn) put(tube string, body string, pri, delay, ttr uint32) (uint64, os.Error) {
	cmd1 := fmt.Sprintf("use %s\r\n", tube)
	p1 := make(chan result)
	o1 := op{cmd1, []string{}, p1}

	cmd2 := fmt.Sprintf("put %d %d %d %d\r\n%s\r\n", pri, delay, ttr, len(body), body)
	p2 := make(chan result)
	o2 := op{cmd2, []string{}, p2}

	c.ch <- []op{o1, o2}

	r1 := <-p1
	if r1.err != nil {
		return 0, Error{c, cmd1, r1.line, r1.err}
	}

	if err, ok := replyErrors[r1.name]; ok {
		return 0, Error{c, cmd1, r1.line, err}
	}

	r2 := <-p2
	if r2.err != nil {
		return 0, Error{c, cmd2, r2.line, r2.err}
	}

	if err, ok := replyErrors[r2.name]; ok {
		return 0, Error{c, cmd2, r2.line, err}
	}

	if r2.name != "INSERTED" {
		return 0, Error{c, cmd2, r2.line, BadReply}
	}

	if len(r2.args) != 1 {
		return 0, Error{c, cmd2, r2.line, BadReply}
	}

	id, err := strconv.Atoui64(r2.args[0])

	if err != nil {
		return 0, Error{c, cmd2, r2.line, BadReply}
	}

	return id, nil
}

func (c Conn) checkForJob(cmd string, r result, s string) (*Job, os.Error) {
	if r.err != nil {
		return nil, Error{c, cmd, r.line, r.err}
	}

	if r.name == "NOT_FOUND" {
		return nil, Error{c, cmd, r.line, NotFound}
	}

	if r.name != s {
		return nil, Error{c, cmd, r.line, BadReply}
	}

	if len(r.args) != 2 {
		return nil, Error{c, cmd, r.line, BadReply}
	}

	id, err := strconv.Atoui64(r.args[0])

	if err != nil {
		return nil, Error{c, cmd, r.line, BadReply}
	}

	return &Job{id, r.body}, nil
}

// Reserve a job from the default tube.
func (c Conn) Peek(id uint64) (*Job, os.Error) {
	cmd := fmt.Sprintf("peek %d\r\n", id)
	p := make(chan result)

	o := op{cmd, []string{}, p}
	c.ch <- []op{o}

	r := <-p
	return c.checkForJob(cmd, r, "FOUND")
}

// A convenient way to submit many jobs to the same tube.
func (c Conn) Tube(name string) Tube {
	return Tube{name, c}
}

func (c Conn) Tubes(names []string) Tubes {
	return Tubes{names, c}
}

// Reserve a job from any one of the tubes in t.
func (t Tubes) Reserve(ttr Usec) (*Job, os.Error) {
	cmd := fmt.Sprintf("reserve-with-timeout %d\r\n", ttr.Seconds())
	p := make(chan result)
	o := op{cmd, t.Names, p}

	t.c.ch <- []op{o}

	r := <-p
	return t.c.checkForJob(cmd, r, "RESERVED")
}

// Delete a job.
func (c Conn) delete(id uint64) os.Error {
	cmd := fmt.Sprintf("delete %d\r\n", id)
	p := make(chan result)

	o := op{cmd, []string{}, p}
	c.ch <- []op{o}

	r := <-p
	if r.err != nil {
		return Error{c, cmd, r.line, r.err}
	}

	if r.name == "NOT_FOUND" {
		return Error{c, cmd, r.line, NotFound}
	}

	if r.name != "DELETED" {
		return Error{c, cmd, r.line, BadReply}
	}

	return nil
}

func (t Tube) Put(body string, pri, delay, ttr uint32) (uint64, os.Error) {
	return t.c.put(t.Name, body, pri, delay, ttr)
}

// Reserve a job from the default tube.
func (t Tube) PeekReady() (*Job, os.Error) {
	cmd := fmt.Sprint("peek-ready\r\n")
	p := make(chan result)

	o := op{cmd, []string{}, p}
	t.c.ch <- []op{o}

	r := <-p
	return t.c.checkForJob(cmd, r, "FOUND")
}

// Reserve a job from the default tube.
func (t Tube) PeekDelayed() (*Job, os.Error) {
	cmd := fmt.Sprint("peek-delayed\r\n")
	p := make(chan result)

	o := op{cmd, []string{}, p}
	t.c.ch <- []op{o}

	r := <-p
	return t.c.checkForJob(cmd, r, "FOUND")
}

func (t Tube) PeekBuried() (*Job, os.Error) {
	cmd := fmt.Sprint("peek-buried\r\n")
	p := make(chan result)

	o := op{cmd, []string{}, p}
	t.c.ch <- []op{o}

	r := <-p
	return t.c.checkForJob(cmd, r, "FOUND")
}

/*
func (j Job) Delete() os.Error {
	return j.c.delete(j.Id)
}
*/
