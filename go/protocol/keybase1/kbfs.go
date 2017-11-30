// Auto-generated by avdl-compiler v1.3.22 (https://github.com/keybase/node-avdl-compiler)
//   Input file: avdl/keybase1/kbfs.avdl

package keybase1

import (
	"github.com/keybase/go-framed-msgpack-rpc/rpc"
	context "golang.org/x/net/context"
)

type FSEventArg struct {
	Event FSNotification `codec:"event" json:"event"`
}

type FSEditListArg struct {
	Edits     []FSNotification `codec:"edits" json:"edits"`
	RequestID int              `codec:"requestID" json:"requestID"`
}

type FSSyncStatusArg struct {
	Status    FSSyncStatus `codec:"status" json:"status"`
	RequestID int          `codec:"requestID" json:"requestID"`
}

type FSSyncEventArg struct {
	Event FSPathSyncStatus `codec:"event" json:"event"`
}

type KbfsInterface interface {
	// Idea is that kbfs would call the function below whenever these actions are
	// performed on a file.
	//
	// Note that this list/interface is very temporary and highly likely to change
	// significantly.
	//
	// It is just a starting point to get kbfs notifications through the daemon to
	// the clients.
	FSEvent(context.Context, FSNotification) error
	// kbfs calls this as a response to receiving an FSEditListRequest with a
	// given requestID.
	FSEditList(context.Context, FSEditListArg) error
	// FSSyncStatus is called by KBFS as a response to receiving an
	// FSSyncStatusRequest with a given requestID.
	FSSyncStatus(context.Context, FSSyncStatusArg) error
	// FSSyncEvent is called by KBFS when the sync status of an individual path
	// changes.
	FSSyncEvent(context.Context, FSPathSyncStatus) error
}

func KbfsProtocol(i KbfsInterface) rpc.Protocol {
	return rpc.Protocol{
		Name: "keybase.1.kbfs",
		Methods: map[string]rpc.ServeHandlerDescription{
			"FSEvent": {
				MakeArg: func() interface{} {
					ret := make([]FSEventArg, 1)
					return &ret
				},
				Handler: func(ctx context.Context, args interface{}) (ret interface{}, err error) {
					typedArgs, ok := args.(*[]FSEventArg)
					if !ok {
						err = rpc.NewTypeError((*[]FSEventArg)(nil), args)
						return
					}
					err = i.FSEvent(ctx, (*typedArgs)[0].Event)
					return
				},
				MethodType: rpc.MethodCall,
			},
			"FSEditList": {
				MakeArg: func() interface{} {
					ret := make([]FSEditListArg, 1)
					return &ret
				},
				Handler: func(ctx context.Context, args interface{}) (ret interface{}, err error) {
					typedArgs, ok := args.(*[]FSEditListArg)
					if !ok {
						err = rpc.NewTypeError((*[]FSEditListArg)(nil), args)
						return
					}
					err = i.FSEditList(ctx, (*typedArgs)[0])
					return
				},
				MethodType: rpc.MethodCall,
			},
			"FSSyncStatus": {
				MakeArg: func() interface{} {
					ret := make([]FSSyncStatusArg, 1)
					return &ret
				},
				Handler: func(ctx context.Context, args interface{}) (ret interface{}, err error) {
					typedArgs, ok := args.(*[]FSSyncStatusArg)
					if !ok {
						err = rpc.NewTypeError((*[]FSSyncStatusArg)(nil), args)
						return
					}
					err = i.FSSyncStatus(ctx, (*typedArgs)[0])
					return
				},
				MethodType: rpc.MethodCall,
			},
			"FSSyncEvent": {
				MakeArg: func() interface{} {
					ret := make([]FSSyncEventArg, 1)
					return &ret
				},
				Handler: func(ctx context.Context, args interface{}) (ret interface{}, err error) {
					typedArgs, ok := args.(*[]FSSyncEventArg)
					if !ok {
						err = rpc.NewTypeError((*[]FSSyncEventArg)(nil), args)
						return
					}
					err = i.FSSyncEvent(ctx, (*typedArgs)[0].Event)
					return
				},
				MethodType: rpc.MethodCall,
			},
		},
	}
}

type KbfsClient struct {
	Cli rpc.GenericClient
}

// Idea is that kbfs would call the function below whenever these actions are
// performed on a file.
//
// Note that this list/interface is very temporary and highly likely to change
// significantly.
//
// It is just a starting point to get kbfs notifications through the daemon to
// the clients.
func (c KbfsClient) FSEvent(ctx context.Context, event FSNotification) (err error) {
	__arg := FSEventArg{Event: event}
	err = c.Cli.Call(ctx, "keybase.1.kbfs.FSEvent", []interface{}{__arg}, nil)
	return
}

// kbfs calls this as a response to receiving an FSEditListRequest with a
// given requestID.
func (c KbfsClient) FSEditList(ctx context.Context, __arg FSEditListArg) (err error) {
	err = c.Cli.Call(ctx, "keybase.1.kbfs.FSEditList", []interface{}{__arg}, nil)
	return
}

// FSSyncStatus is called by KBFS as a response to receiving an
// FSSyncStatusRequest with a given requestID.
func (c KbfsClient) FSSyncStatus(ctx context.Context, __arg FSSyncStatusArg) (err error) {
	err = c.Cli.Call(ctx, "keybase.1.kbfs.FSSyncStatus", []interface{}{__arg}, nil)
	return
}

// FSSyncEvent is called by KBFS when the sync status of an individual path
// changes.
func (c KbfsClient) FSSyncEvent(ctx context.Context, event FSPathSyncStatus) (err error) {
	__arg := FSSyncEventArg{Event: event}
	err = c.Cli.Call(ctx, "keybase.1.kbfs.FSSyncEvent", []interface{}{__arg}, nil)
	return
}
