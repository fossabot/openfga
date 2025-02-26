package test

import (
	"context"
	"testing"

	"github.com/openfga/openfga/pkg/encoder"
	"github.com/openfga/openfga/pkg/id"
	"github.com/openfga/openfga/pkg/logger"
	"github.com/openfga/openfga/pkg/testutils"
	"github.com/openfga/openfga/server/commands"
	serverErrors "github.com/openfga/openfga/server/errors"
	"github.com/openfga/openfga/storage"
	teststorage "github.com/openfga/openfga/storage/test"
	"github.com/stretchr/testify/require"
	openfgapb "go.buf.build/openfga/go/openfga/api/openfga/v1"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestReadAuthorizationModelsWithoutPaging(t *testing.T, dbTester teststorage.DatastoreTester[storage.OpenFGADatastore]) {
	store := testutils.CreateRandomString(20)

	require := require.New(t)
	logger := logger.NewNoopLogger()
	encoder := encoder.NewNoopEncoder()
	ctx := context.Background()

	datastore, err := dbTester.New()
	require.NoError(err)

	tests := []struct {
		name                      string
		backendState              map[string]*openfgapb.TypeDefinitions
		request                   *openfgapb.ReadAuthorizationModelsRequest
		expectedNumModelsReturned int
	}{
		{
			name: "empty",
			request: &openfgapb.ReadAuthorizationModelsRequest{
				StoreId: store,
			},
			expectedNumModelsReturned: 0,
		},
		{
			name: "empty for requested store",
			backendState: map[string]*openfgapb.TypeDefinitions{
				"another-store": {
					TypeDefinitions: []*openfgapb.TypeDefinition{},
				},
			},
			request: &openfgapb.ReadAuthorizationModelsRequest{
				StoreId: store,
			},
			expectedNumModelsReturned: 0,
		},
		{
			name: "multiple type definitions",
			backendState: map[string]*openfgapb.TypeDefinitions{
				store: {
					TypeDefinitions: []*openfgapb.TypeDefinition{
						{
							Type: "ns1",
						},
					},
				},
			},
			request: &openfgapb.ReadAuthorizationModelsRequest{
				StoreId: store,
			},
			expectedNumModelsReturned: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.backendState != nil {
				for store, state := range test.backendState {
					modelID, err := id.NewString()
					require.NoError(err)
					if err := datastore.WriteAuthorizationModel(ctx, store, modelID, state); err != nil {
						t.Fatalf("WriteAuthorizationModel(%s), err = %v, want nil", store, err)
					}
				}
			}

			query := commands.NewReadAuthorizationModelsQuery(datastore, encoder, logger)
			resp, err := query.Execute(ctx, test.request)

			require.NoError(err)
			require.Equal(test.expectedNumModelsReturned, len(resp.GetAuthorizationModels()))
			require.Empty(resp.ContinuationToken, "expected an empty continuation token")
		})
	}
}

func TestReadAuthorizationModelsWithPaging(t *testing.T, dbTester teststorage.DatastoreTester[storage.OpenFGADatastore]) {
	require := require.New(t)
	ctx := context.Background()
	logger := logger.NewNoopLogger()

	datastore, err := dbTester.New()
	require.NoError(err)

	tds := &openfgapb.TypeDefinitions{
		TypeDefinitions: []*openfgapb.TypeDefinition{
			{
				Type: "ns1",
			},
		},
	}

	store := testutils.CreateRandomString(10)
	modelID1, err := id.NewString()
	require.NoError(err)

	if err := datastore.WriteAuthorizationModel(ctx, store, modelID1, tds); err != nil {
		t.Fatal(err)
	}
	modelID2, err := id.NewString()
	if err != nil {
		t.Fatal(err)
	}
	if err := datastore.WriteAuthorizationModel(ctx, store, modelID2, tds); err != nil {
		t.Fatal(err)
	}

	encoder, err := encoder.NewTokenEncrypter("key")
	require.NoError(err)

	query := commands.NewReadAuthorizationModelsQuery(datastore, encoder, logger)
	firstRequest := &openfgapb.ReadAuthorizationModelsRequest{
		StoreId:  store,
		PageSize: wrapperspb.Int32(1),
	}
	firstResponse, err := query.Execute(ctx, firstRequest)
	require.NoError(err)
	require.Len(firstResponse.AuthorizationModels, 1)
	require.Equal(firstResponse.AuthorizationModels[0].Id, modelID2)
	require.NotEmpty(firstResponse.ContinuationToken, "Expected continuation token")

	secondRequest := &openfgapb.ReadAuthorizationModelsRequest{
		StoreId:           store,
		PageSize:          wrapperspb.Int32(1),
		ContinuationToken: firstResponse.ContinuationToken,
	}
	secondResponse, err := query.Execute(ctx, secondRequest)
	require.NoError(err)
	require.Len(secondResponse.AuthorizationModels, 1)
	require.Equal(secondResponse.AuthorizationModels[0].Id, modelID1)
	require.Empty(secondResponse.ContinuationToken, "Expected empty continuation token")

	thirdRequest := &openfgapb.ReadAuthorizationModelsRequest{
		StoreId:           store,
		ContinuationToken: "bad",
	}
	_, err = query.Execute(ctx, thirdRequest)
	require.Error(err)
	require.ErrorContains(err, "Invalid continuation token")

	validToken := "eyJwayI6IkxBVEVTVF9OU0NPTkZJR19hdXRoMHN0b3JlIiwic2siOiIxem1qbXF3MWZLZExTcUoyN01MdTdqTjh0cWgifQ=="
	invalidStoreRequest := &openfgapb.ReadAuthorizationModelsRequest{
		StoreId:           "non-existent",
		ContinuationToken: validToken,
	}
	_, err = query.Execute(ctx, invalidStoreRequest)
	require.Error(err)
	require.ErrorContains(err, "Invalid continuation token")
}

func TestReadAuthorizationModelsInvalidContinuationToken(t *testing.T, dbTester teststorage.DatastoreTester[storage.OpenFGADatastore]) {
	require := require.New(t)
	ctx := context.Background()
	logger := logger.NewNoopLogger()

	datastore, err := dbTester.New()
	require.NoError(err)

	store := testutils.CreateRandomString(10)
	modelID, err := id.NewString()
	if err != nil {
		t.Fatal(err)
	}
	tds := &openfgapb.TypeDefinitions{
		TypeDefinitions: []*openfgapb.TypeDefinition{
			{
				Type: "repo",
			},
		},
	}

	if err := datastore.WriteAuthorizationModel(ctx, store, modelID, tds); err != nil {
		t.Fatal(err)
	}
	encoder, err := encoder.NewTokenEncrypter("key")
	require.NoError(err)

	_, err = commands.NewReadAuthorizationModelsQuery(datastore, encoder, logger).Execute(ctx, &openfgapb.ReadAuthorizationModelsRequest{
		StoreId:           store,
		ContinuationToken: "foo",
	})
	require.ErrorIs(err, serverErrors.InvalidContinuationToken)
}
