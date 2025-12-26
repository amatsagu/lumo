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
	l.mu.RLock()
	if l.workerActive {
		l.mu.RUnlock()
		return
	}
	l.mu.RUnlock()

	l.mu.Lock()
	if l.workerActive {
		l.mu.Unlock()
		return
	}

	l.logQueue = make(chan logTask, queueSize)
	l.wg.Add(1)
	l.workerActive = true
	go l.processQueue()

	l.mu.Unlock()
}

func (l *logger) stopWorker() {
	l.mu.Lock()
	if l.workerActive {
		close(l.logQueue)
		l.workerActive = false
		l.mu.Unlock()

		l.wg.Wait()

		l.mu.Lock()
		l.logQueue = nil
	}
	l.mu.Unlock()
}

func (l *logger) enqueue(task logTask) {
	l.ensureWorkerStarted()

	l.mu.RLock()

	if !l.workerActive || l.logQueue == nil {
		l.mu.RUnlock()
		return
	}

	select {
	case l.logQueue <- task:
	default:
		l.logQueue <- task
	}

	l.mu.RUnlock()
}

func (l *logger) processQueue() {
	// Acquire lock to read configuration safely.
	// This guarantees we see the latest SetOutput() call.
	l.mu.Lock()
	currentOutput := l.output
	l.mu.Unlock()

	writer := bufio.NewWriter(currentOutput)
	timer := time.NewTimer(workerCooldown)

	for {
		select {
		case task, ok := <-l.logQueue:
			if !ok {
				timer.Stop()
				writer.Flush()
				l.wg.Done()
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
			for range n {
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

			timer.Stop()
			writer.Flush()
			l.wg.Done()
			return
		}
	}
}

func writeLog(w *bufio.Writer, task logTask) {
	l.mu.RLock()
	timeFormat := l.timeFormat
	l.mu.RUnlock()

	fmt.Fprintf(w, "%s%s%s %s%-5s%s %s%s:%d%s %s\n",
		cGray, task.time.Format(timeFormat), cReset,
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
