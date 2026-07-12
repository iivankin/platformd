package state

import (
	"context"
	"database/sql"
	"fmt"
)

type writeRequest struct {
	context context.Context
	action  func(*sql.Tx) error
	result  chan error
}

type writer struct {
	database *sql.DB
	requests chan writeRequest
	context  context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

func newWriter(database *sql.DB) *writer {
	ctx, cancel := context.WithCancel(context.Background())
	writer := &writer{
		database: database,
		requests: make(chan writeRequest, writerQueueSize),
		context:  ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go writer.run()
	return writer
}

func (writer *writer) Close() {
	writer.cancel()
	<-writer.done
}

func (writer *writer) Transaction(ctx context.Context, action func(*sql.Tx) error) error {
	request := writeRequest{context: ctx, action: action, result: make(chan error, 1)}
	select {
	case writer.requests <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-writer.context.Done():
		return ErrClosed
	}

	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-writer.context.Done():
		return ErrClosed
	}
}

func (writer *writer) run() {
	defer close(writer.done)
	for {
		select {
		case <-writer.context.Done():
			return
		case request := <-writer.requests:
			request.result <- writer.execute(request)
		}
	}
}

func (writer *writer) execute(request writeRequest) error {
	if err := request.context.Err(); err != nil {
		return err
	}
	transaction, err := writer.database.BeginTx(request.context, nil)
	if err != nil {
		return fmt.Errorf("begin state transaction: %w", err)
	}
	if err := request.action(transaction); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit state transaction: %w", err)
	}
	return nil
}
