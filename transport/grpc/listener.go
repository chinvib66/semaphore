package grpc

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"sync"

	"github.com/jexia/maestro/codec"
	"github.com/jexia/maestro/codec/proto"
	"github.com/jexia/maestro/instance"
	"github.com/jexia/maestro/logger"
	"github.com/jexia/maestro/specs"
	"github.com/jexia/maestro/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

// NewListener constructs a new listener for the given addr
func NewListener(addr string, opts specs.Options) transport.NewListener {
	// options, err := ParseListenerOptions(opts)
	// if err != nil {
	// 	// TODO: log err
	// }

	return func(ctx instance.Context) transport.Listener {
		return &Listener{
			addr: addr,
			ctx:  ctx,
		}
	}
}

// Listener represents a HTTP listener
type Listener struct {
	addr     string
	ctx      instance.Context
	server   *grpc.Server
	methods  map[string]*Method
	services map[string]*Service
	mutex    sync.RWMutex
}

// Name returns the name of the given listener
func (listener *Listener) Name() string {
	return "grpc"
}

// Serve opens the HTTP listener and calls the given handler function on reach request
func (listener *Listener) Serve() error {
	listener.ctx.Logger(logger.Transport).WithField("addr", listener.addr).Info("Serving gRPC listener")

	listener.server = grpc.NewServer(
		grpc.CustomCodec(Codec()),
		grpc.UnknownServiceHandler(listener.handler),
	)

	rpb.RegisterServerReflectionServer(listener.server, listener)

	lis, err := net.Listen("tcp", listener.addr)
	if err != nil {
		return err
	}

	err = listener.server.Serve(lis)
	if err != nil {
		return err
	}

	return nil
}

// Handle parses the given endpoints and constructs route handlers
func (listener *Listener) Handle(endpoints []*transport.Endpoint, codecs map[string]codec.Constructor) error {
	logger := listener.ctx.Logger(logger.Transport)
	logger.Info("gRPC listener received new endpoints")

	constructor := proto.NewConstructor()
	methods := make(map[string]*Method, len(endpoints))
	services := map[string]*Service{}

	for _, endpoint := range endpoints {
		options, err := ParseEndpointOptions(endpoint)
		if err != nil {
			return err
		}

		req, err := constructor.New(specs.InputResource, endpoint.Request)
		if err != nil {
			return err
		}

		res, err := constructor.New(specs.OutputResource, endpoint.Response)
		if err != nil {
			return err
		}

		service := fmt.Sprintf("%s.%s", options.Package, options.Service)
		name := fmt.Sprintf("%s/%s", service, options.Method)

		methods[name] = &Method{
			fqn:  name,
			name: options.Method,
			flow: endpoint.Flow,
			in:   endpoint.Request,
			req:  req,
			out:  endpoint.Response,
			res:  res,
		}

		if services[service] == nil {
			services[service] = &Service{
				pkg:     options.Package,
				name:    options.Service,
				methods: map[string]*Method{},
			}
		}

		services[service].methods[name] = methods[name]
	}

	file := proto.NewFile("maestro")
	file.Package = "maestro.greeter"

	for _, service := range services {
		methods := make(proto.Methods, len(service.methods))

		for key, method := range service.methods {
			methods[key] = method
		}

		err := proto.NewServiceDescriptor(file, service.name, methods)
		if err != nil {
			return err
		}
	}

	result, err := file.Build()
	if err != nil {
		return err
	}

	for _, service := range services {
		service.file = result.AsFileDescriptorProto()
	}

	listener.mutex.Lock()
	listener.methods = methods
	listener.services = services
	listener.mutex.Unlock()

	return nil
}

func (listener *Listener) handler(srv interface{}, stream grpc.ServerStream) error {
	listener.mutex.RLock()
	defer listener.mutex.RUnlock()

	fqn, ok := grpc.MethodFromServerStream(stream)
	if !ok {
		return grpc.Errorf(codes.Internal, "low level server stream not exists in context")
	}

	_, ok = metadata.FromIncomingContext(stream.Context())
	if ok {
		// TODO: support header values
	}

	method := listener.methods[fqn[1:]]
	if method == nil {
		return grpc.Errorf(codes.Unimplemented, "unknown method: %s", fqn)
	}

	req := &frame{}
	err := stream.RecvMsg(req)
	if err != nil {
		return err
	}

	store := method.flow.NewStore()
	err = method.req.Unmarshal(bytes.NewBuffer(req.payload), store)
	if err != nil {
		return grpc.Errorf(codes.ResourceExhausted, "invalid message body: %s", err)
	}

	err = method.flow.Call(stream.Context(), store)
	if err != nil {
		return grpc.Errorf(codes.Internal, "unkown error: %s", err)
	}

	reader, err := method.res.Marshal(store)
	if err != nil {
		return grpc.Errorf(codes.ResourceExhausted, "invalid response body: %s", err)
	}

	bb, err := ioutil.ReadAll(reader)
	if err != nil {
		return grpc.Errorf(codes.ResourceExhausted, "unable to read full response body: %s", err)
	}

	res := &frame{
		payload: bb,
	}

	err = stream.SendMsg(res)
	if err != nil {
		return grpc.Errorf(codes.Internal, "unkown error: %s", err)
	}

	stream.SetTrailer(metadata.MD{})

	return nil
}

// Close closes the given listener
func (listener *Listener) Close() error {
	listener.ctx.Logger(logger.Transport).Info("Closing gRPC listener")
	listener.server.GracefulStop()
	return nil
}
