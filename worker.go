package lumo

import (
	"bufio"
	"fmt"
	"time"
)

const (
	queueSize      = 4096
	workerCooldown = 10 * time.Second
)

type logTask struct {
	level      level
	levelColor string
	label      string
	time       time.Time
	file       string
	line       int
	msg        string
	stack      []byte
	context    []contextItem
}

func (l *logger) ensureWorkerStarted() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.workerActive {
		return
	}

	l.logQueue = make(chan logTask, queueSize)
	l.workerActive = true
	l.wg.Add(1)
	go l.processQueue()
}

func (l *logger) stopWorker() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.workerActive {
		close(l.logQueue)
		l.workerActive = false
		l.mu.Unlock()
		l.wg.Wait()
		l.mu.Lock()
		l.logQueue = nil
	}
}

func (l *logger) enqueue(task logTask) {
	l.ensureWorkerStarted()

	defer func() { recover() }()

	select {
	case l.logQueue <- task:
	default:
		l.logQueue <- task
	}
}

func (l *logger) processQueue() {
	defer l.wg.Done()

	// FIX: Acquire lock to read configuration safely.
	// This guarantees we see the latest SetOutput() call.
	l.mu.Lock()
	currentOutput := l.output
	l.mu.Unlock()

	writer := bufio.NewWriter(currentOutput)
	timer := time.NewTimer(workerCooldown)

	defer func() {
		timer.Stop()
		writer.Flush()
	}()

	for {
		select {
		case task, ok := <-l.logQueue:
			if !ok {
				return
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(workerCooldown)

			writeLog(writer, task)

			n := len(l.logQueue)
			for i := 0; i < n; i++ {
				writeLog(writer, <-l.logQueue)
			}
			writer.Flush()

		case <-timer.C:
			l.mu.Lock()
			if len(l.logQueue) > 0 {
				l.mu.Unlock()
				timer.Reset(workerCooldown)
				continue
			}
			l.workerActive = false
			l.logQueue = nil
			l.mu.Unlock()
			return
		}
	}
}

func writeLog(w *bufio.Writer, task logTask) {
	ts := task.time.Format("02/01/2006 15:04:05 UTC")

	fmt.Fprintf(w, "%s%s%s %s%-5s%s %s%s:%d%s %s\n",
		cGray, ts, cReset,
		task.levelColor, task.label, cReset,
		cGray, task.file, task.line, cReset,
		task.msg,
	)

	if len(task.context) > 0 {
		fmt.Fprintf(w, "%s   included context:%s\n", cGray, cReset)
		for _, item := range task.context {
			fmt.Fprintf(w, "%s      %s:%s %+v%s\n",
				cWhite, item.Label,
				cGray, item.Value,
				cReset,
			)
		}
	}

	if task.stack != nil {
		printParsedStack(w, task.stack)
	}
}
