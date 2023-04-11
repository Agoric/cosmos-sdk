package modjewel

import (
	"context"
	"errors"
	"reflect"

	grpc1 "github.com/cosmos/gogoproto/grpc"
	grpc "google.golang.org/grpc"
)

var (
	ErrBadMethodOrService = errors.New("bad method or service")
	ErrUnimplemented      = errors.New("unimplemented")
)

// Connection is the GRPC equivalent of an ethernet "crossover" cable,
// allowing a client to talk directly to a server with no intervening routing.
type Connection struct {
	// server is the object implementing the server side of the service
	server interface{}

	// methodDescs is a map of fully-qualified gRPC method name to method descriptors.
	// Note: we'd like to map to just grpc.methodHanlder, but it's not an exported type.
	methodDescs map[string]grpc.MethodDesc
}

var _ grpc1.Server = &Connection{}

// NewConnection returns a new Connection which should be used to provide ocap access
// to the implementation of a single gRPC serice.
func NewConnection() *Connection {
	c := Connection{}
	return &c
}

func (c *Connection) RegisterService(sd *grpc.ServiceDesc, ss interface{}) {
	c.server = ss
	for _, md := range sd.Methods {
		fqmn := "/" + sd.ServiceName + "/" + md.MethodName
		c.methodDescs[fqmn] = md
	}
}

// MakeLocalClient returns a gRPC client connection for a local server that avoids marshaling.
// The returned client isolates the server-side data via a closure so that the server-side
// data cannot be accessed via casting or reflection. The only access is through the
// configured service interface.
func (c *Connection) MakeLocalClient() grpc1.ClientConn {
	return client{
		invokeFunc: c.invoke,
	}
}

type client struct {
	invokeFunc func(ctx context.Context, method string, args, reply interface{}, opts ...grpc.CallOption) error
}

func (c client) Invoke(ctx context.Context, method string, args, reply interface{}, opts ...grpc.CallOption) error {
	return c.invokeFunc(ctx, method, args, reply, opts...)
}

func (c client) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, ErrUnimplemented
}

func (c *Connection) invoke(ctx context.Context, method string, args, reply interface{}, opts ...grpc.CallOption) error {
	md, found := c.methodDescs[method]
	if !found {
		return ErrBadMethodOrService
	}
	res, err := md.Handler(c.server, ctx, func(interface{}) error { return nil }, nil)
	if err != nil {
		return err
	}
	// Note: the generated client and server interfaces have both sides provide a structure.
	// If the client interface just returned a result, we could pass res right through.
	// Instead, we need to use a magic copy.
	err = copy(reply, res)
	if err != nil {
		return err
	}
	return nil
}

// copy copies the struct pointed to by dst to the struct pointed to by src,
// which must be structures of the same type.
func copy(dst, src interface{}) error {
	// we'll use reflection to avoid marshaling
	dstVal := reflect.ValueOf(dst)
	srcVal := reflect.ValueOf(src)
	if dstVal.Kind() != reflect.Pointer || dstVal.IsNil() || dstVal.Elem().Kind() != reflect.Struct {
		return errors.New("destionation must be a non-nil pointer to a struct")
	}
	if srcVal.Kind() != reflect.Pointer || srcVal.IsNil() || srcVal.Elem().Kind() != reflect.Struct {
		return errors.New("source must be a non-nil pointer to a struct")
	}
	dstElem := dstVal.Elem()
	srcElem := srcVal.Elem()
	if srcElem.Type() != dstElem.Type() {
		return errors.New("source and destination must point to the same type")
	}
	if !dstElem.CanSet() {
		return errors.New("cannot set destination value")
	}
	srcElem.Set(dstElem)
	return nil
}
