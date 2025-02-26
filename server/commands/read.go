package commands

import (
	"context"

	"github.com/go-errors/errors"
	"github.com/openfga/openfga/pkg/encoder"
	"github.com/openfga/openfga/pkg/logger"
	tupleUtils "github.com/openfga/openfga/pkg/tuple"
	"github.com/openfga/openfga/pkg/utils"
	serverErrors "github.com/openfga/openfga/server/errors"
	"github.com/openfga/openfga/storage"
	openfgapb "go.buf.build/openfga/go/openfga/api/openfga/v1"
	"go.opentelemetry.io/otel/trace"
)

// A ReadQuery can be used to read one or many tuplesets
// Each tupleset specifies keys of a set of relation tuples.
// The set can include a single tuple key, or all tuples with
// a given object ID or userset in a type, optionally
// constrained by a relation name.
type ReadQuery struct {
	logger    logger.Logger
	tracer    trace.Tracer
	datastore storage.OpenFGADatastore
	encoder   encoder.Encoder
}

// NewReadQuery creates a ReadQuery using the provided OpenFGA datastore implementation.
func NewReadQuery(datastore storage.OpenFGADatastore, tracer trace.Tracer, logger logger.Logger, encoder encoder.Encoder) *ReadQuery {
	return &ReadQuery{
		logger:    logger,
		tracer:    tracer,
		datastore: datastore,
		encoder:   encoder,
	}
}

// Execute the ReadQuery, returning paginated `openfga.Tuple`(s) that match the tupleset
func (q *ReadQuery) Execute(ctx context.Context, req *openfgapb.ReadRequest) (*openfgapb.ReadResponse, error) {
	store := req.GetStoreId()
	modelID := req.GetAuthorizationModelId()
	tk := req.GetTupleKey()
	decodedContToken, err := q.encoder.Decode(req.GetContinuationToken())
	if err != nil {
		return nil, serverErrors.InvalidContinuationToken
	}
	paginationOptions := storage.NewPaginationOptions(req.GetPageSize().GetValue(), string(decodedContToken))

	dbCallsCounter := utils.NewDBCallCounter()
	if err := q.validateAndAuthenticateTupleset(ctx, store, modelID, tk, dbCallsCounter); err != nil {
		utils.LogDBStats(ctx, q.logger, "Read", dbCallsCounter.GetReadCalls(), 0)
		return nil, err
	}

	dbCallsCounter.AddReadCall()
	utils.LogDBStats(ctx, q.logger, "Read", dbCallsCounter.GetReadCalls(), 0)
	tuples, contToken, err := q.datastore.ReadPage(ctx, store, tk, paginationOptions)
	if err != nil {
		return nil, serverErrors.HandleError("", err)
	}

	encodedContToken, err := q.encoder.Encode(contToken)
	if err != nil {
		return nil, serverErrors.HandleError("", err)
	}

	return &openfgapb.ReadResponse{
		Tuples:            tuples,
		ContinuationToken: encodedContToken,
	}, nil
}

func (q *ReadQuery) validateAndAuthenticateTupleset(ctx context.Context, store, authorizationModelID string, tupleKey *openfgapb.TupleKey, rwCounter utils.DBCallCounter) error {
	ctx, span := q.tracer.Start(ctx, "validateAndAuthenticateTupleset")
	defer span.End()

	objectType, objectID := tupleUtils.SplitObject(tupleKey.GetObject())
	if objectType == "" {
		return serverErrors.InvalidTupleSet
	}

	// at this point we "think" we have a type. before a backend query, we validate things we can check locally
	if objectID == "" && tupleKey.GetUser() == "" {
		return serverErrors.InvalidTuple("missing objectID and user", tupleKey)
	}

	rwCounter.AddReadCall()

	ns, err := q.datastore.ReadTypeDefinition(ctx, store, authorizationModelID, objectType)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return serverErrors.TypeNotFound(objectType)
		}
		return serverErrors.HandleError("", err)
	}

	if tupleKey.GetRelation() != "" {
		_, ok := ns.Relations[tupleKey.GetRelation()]
		if !ok {
			return serverErrors.RelationNotFound(tupleKey.GetRelation(), ns.GetType(), tupleKey)
		}
	}

	return nil
}
