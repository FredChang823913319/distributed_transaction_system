package server

import (
	"errors"
	"fmt"
	"sync"

	"../shared"
)

// Server Node
type Server struct {
	ID      string
	Objects map[string]Object
}

// NewServer : Server Node constructor
func NewServer(id string) *Server {
	server := new(Server)
	server.ID = id
	server.Objects = make(map[string]Object)
	return server
}

// Object : single object in server
type Object struct {
	Value        string
	Readers      *shared.StringSet // Transaction ID
	Writer       string            // Transaction ID
	RequestQueue []*LockRequest
	m            sync.Mutex
}

// NewObject : Server Object constructor
func NewObject(value string) *Object {
	object := new(Object)
	object.Value = value
	object.Readers = shared.NewSet()
	object.Writer = ""
	object.RequestQueue = make([]*LockRequest, 0)
	return object
}

// LockRequest : single request for requiring a lock
type LockRequest struct {
	Type          string // read, write, upgrade
	TransactionID string
	Channel       chan bool // granted, abort
}

// NewLockRequest : LockRequest constructor
func NewLockRequest(t string, tid string) *LockRequest {
	req := new(LockRequest)
	req.Type = t
	req.TransactionID = tid
	req.Channel = make(chan bool)
	return req
}

// ReleaseLock : release lock on object for transaction
func (server *Server) ReleaseLock(args *shared.Args, reply *string) error {
	obj, found := server.Objects[args.Key]
	if !found {
		return errors.New("ReleaseLock: Object key=" + args.Key + " not found")
	}
	isReader := obj.Readers.SetDelete(args.TransactionID)
	isWriter := obj.Writer == args.TransactionID
	if !isReader && !isWriter {
		return errors.New("ReleaseLock: Object key=" + args.Key + " not locked by transaction " + args.TransactionID)
	}
	if isWriter {
		obj.Writer = ""
	}
	*reply = "SUCCESS"
	// TODO: Grant lock to next request(s) in queue

	return nil
}

// WriterLock : Get Write lock of the corresponding object
func (server *Server) WriterLock(args *shared.Args, reply *string) error {
	obj, found := server.Objects[args.Key]

	// New object immediately gets writer access
	if !found {
		*reply = "SUCCESS"
		return nil
	}

	obj.m.Lock()
	// No Writer
	if obj.Writer == "" {
		// No reader/writer, immediately grant
		if obj.Readers.Size() == 0 {
			obj.Writer = args.TransactionID
			*reply = "SUCCESS"
			obj.m.Unlock()
		} else {
			// No writer, has readers
			// Client transaction is already reading,
			if obj.Readers.SetHas(args.TransactionID) {
				// Client transaction is the only reader
				if obj.Readers.Size() == 1 {
					// Immediately Promote
					obj.Writer = args.TransactionID
					obj.Readers.SetDelete(args.TransactionID)
					*reply = "SUCCESS"
					obj.m.Unlock()
				} else {
					// Client transaction is not the only reader
					// Wait until transaction is the only reader, then promote
					req := NewLockRequest("promote", args.TransactionID)
					obj.RequestQueue = append([]*LockRequest{req}, obj.RequestQueue...) // Prepend to queue
					obj.m.Unlock()
					// Wait for grant/abort
					ok := <-req.Channel
					if ok {
						*reply = "SUCCESS"
					} else {
						*reply = "ABORT"
					}
				}
			} else {
				// Client transaction is not reader, wait for releasing of all read locks
				req := NewLockRequest("write", args.TransactionID)
				obj.RequestQueue = append(obj.RequestQueue, req)
				obj.m.Unlock()
				// Wait for grant/abort
				ok := <-req.Channel
				if ok {
					*reply = "SUCCESS"
				} else {
					*reply = "ABORT"
				}
			}
		}
	} else {
		// Write lock is hold by other, wait for releasing of writer lock (There should be 0 readers)
		if obj.Readers.Size() != 0 {
			fmt.Println("Reader-Writer Conflict!")
			obj.m.Unlock()
			return errors.New("Write: Object key=" + args.Key + ", Transaction=" + args.TransactionID + ". Reader-writer conflict.")
		}
		req := NewLockRequest("write", args.TransactionID)
		obj.RequestQueue = append(obj.RequestQueue, req)
		obj.m.Unlock()
		// Wait for grant/abort
		ok := <-req.Channel
		if ok {
			*reply = "SUCCESS"
		} else {
			*reply = "ABORT"
		}
	}
	return nil
}

// Read : Get Read lock of the corresponding object and send back read value if read clock is acquired.
// @Reply: 1. SUCCESS + res => grant read lock and send back read value, seperate by " ", example: "SUCCESS A.h 5"
//         2. NOT FOUND => No request object is found, client should abort the transacion
// 		   3. ABORT => server decied to abort the transaction due to deadlock
func (server *Server) Read(args *shared.Args, reply *string) error {
	obj, found := server.Objects[args.Key]

	if !found {
		*reply = "NOT FOUND"
		return nil
	}

	obj.m.Lock()
	if obj.Writer != "" && obj.Readers.Size() == 0 {
		// No reader/writer, grant
		obj.Readers.SetAdd(args.TransactionID)
		*reply = "SUCCESS"
		obj.m.Unlock()
	} else if obj.Readers.Size() > 0 && obj.Writer == "" {
		// Has readers, no writer
		// Grant only if no queued writer (writer-preferring RW lock)
		if len(obj.RequestQueue) == 0 {
			obj.Readers.SetAdd(args.TransactionID)
			*reply = "SUCCESS"
			obj.m.Unlock()
		} else {
			req := NewLockRequest("read", args.TransactionID)
			obj.RequestQueue = append(obj.RequestQueue, req)
			obj.m.Unlock()
		}
	} else if obj.Readers.Size() == 0 && obj.Writer != "" {
		// No readers, has writer
		req := NewLockRequest("read", args.TransactionID)
		obj.RequestQueue = append(obj.RequestQueue, req)
		obj.m.Unlock()
	} else {
		// Both readers and writers, conflict
		if obj.Readers.Size() != 0 {
			fmt.Println("Reader-Writer Conflict!")
		}
		obj.m.Unlock()
		return errors.New("Read: Object key=" + args.Key + ", Transaction=" + args.TransactionID + ". Reader-writer conflict.")
	}

	return nil
}

// Set : Perform the update (on commit)
// func (server *Server) Set(args *shared.Args, reply *string) error {

// 	return nil
// }

// func (server *Server) Commit(args *shared.Args, reply *string) error {

// 	return nil
// }

// func (server *Server) Abort(args *shared.Args, reply *string) error {

// 	return nil
// }

// type Quotient struct {
// 	Quo, Rem int
// }

// type Arith int

// func (t *Arith) Multiply(args *Args, reply *int) error {
// 	*reply = args.A * args.B
// 	return nil
// }

// func (t *Arith) Divide(args *Args, quo *Quotient) error {
// 	if args.B == 0 {
// 		return errors.New("divide by zero")
// 	}
// 	quo.Quo = args.A / args.B
// 	quo.Rem = args.A % args.B
// 	return nil
// }
