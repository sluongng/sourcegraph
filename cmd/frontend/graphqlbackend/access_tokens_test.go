package graphqlbackend

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/graph-gophers/graphql-go"
	gqlerrors "github.com/graph-gophers/graphql-go/errors"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/backend"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/envvar"
	"github.com/sourcegraph/sourcegraph/internal/actor"
	"github.com/sourcegraph/sourcegraph/internal/authz"
	"github.com/sourcegraph/sourcegraph/internal/conf"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/database/dbtesting"
	"github.com/sourcegraph/sourcegraph/internal/types"
	"github.com/sourcegraph/sourcegraph/schema"
)

// 🚨 SECURITY: This tests that users can't create tokens for users they aren't allowed to do so for.
func TestMutation_CreateAccessToken(t *testing.T) {
	db := new(dbtesting.MockDB)

	mockAccessTokensCreate := func(t *testing.T, wantCreatorUserID int32, wantScopes []string) {
		database.Mocks.AccessTokens.Create = func(subjectUserID int32, scopes []string, note string, creatorUserID int32) (int64, string, error) {
			if want := int32(1); subjectUserID != want {
				t.Errorf("got %v, want %v", subjectUserID, want)
			}
			if !reflect.DeepEqual(scopes, wantScopes) {
				t.Errorf("got %q, want %q", scopes, wantScopes)
			}
			if want := "n"; note != want {
				t.Errorf("got %q, want %q", note, want)
			}
			if creatorUserID != wantCreatorUserID {
				t.Errorf("got %v, want %v", creatorUserID, wantCreatorUserID)
			}
			return 1, "t", nil
		}
	}

	const uid1GQLID = "VXNlcjox"

	t.Run("authenticated as user", func(t *testing.T) {
		resetMocks()
		mockAccessTokensCreate(t, 1, []string{authz.ScopeUserAll})
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) {
			return &types.User{ID: 1, SiteAdmin: false}, nil
		}
		RunTests(t, []*Test{
			{
				Context: actor.WithActor(context.Background(), &actor.Actor{UID: 1}),
				Schema:  mustParseGraphQLSchema(t),
				Query: `
				mutation {
					createAccessToken(user: "` + uid1GQLID + `", scopes: ["user:all"], note: "n") {
						id
						token
					}
				}
			`,
				ExpectedResult: `
				{
					"createAccessToken": {
						"id": "QWNjZXNzVG9rZW46MQ==",
						"token": "t"
					}
				}
			`,
			},
		})
	})

	t.Run("authenticated as user, using invalid scopes", func(t *testing.T) {
		resetMocks()

		ctx := actor.WithActor(context.Background(), &actor.Actor{UID: 1})
		result, err := (&schemaResolver{db: db}).CreateAccessToken(ctx, &createAccessTokenInput{User: uid1GQLID /* no scopes */, Note: "n"})
		if err == nil {
			t.Error("err == nil")
		}
		if result != nil {
			t.Errorf("got result %v, want nil", result)
		}
	})

	t.Run("authenticated as user, using site-admin-only scopes", func(t *testing.T) {
		resetMocks()
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) {
			return &types.User{ID: 1, SiteAdmin: false}, nil
		}

		ctx := actor.WithActor(context.Background(), &actor.Actor{UID: 1})
		result, err := (&schemaResolver{db: db}).CreateAccessToken(ctx, &createAccessTokenInput{
			User:   uid1GQLID,
			Scopes: []string{authz.ScopeUserAll, authz.ScopeSiteAdminSudo},
			Note:   "n",
		})
		if want := backend.ErrMustBeSiteAdmin; err != want {
			t.Errorf("got err %v, want %v", err, want)
		}
		if result != nil {
			t.Errorf("got result %v, want nil", result)
		}
	})

	t.Run("authenticated as site admin, using site-admin-only scopes", func(t *testing.T) {
		resetMocks()
		mockAccessTokensCreate(t, 1, []string{authz.ScopeSiteAdminSudo, authz.ScopeUserAll})
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) {
			return &types.User{ID: 1, SiteAdmin: true}, nil
		}
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()

		RunTests(t, []*Test{
			{
				Context: actor.WithActor(context.Background(), &actor.Actor{UID: 1}),
				Schema:  mustParseGraphQLSchema(t),
				Query: `
				mutation {
					createAccessToken(user: "` + uid1GQLID + `", scopes: ["user:all", "site-admin:sudo"], note: "n") {
						id
						token
					}
				}
			`,
				ExpectedResult: `
				{
					"createAccessToken": {
						"id": "QWNjZXNzVG9rZW46MQ==",
						"token": "t"
					}
				}
			`,
			},
		})
	})

	t.Run("authenticated as different user who is a site-admin. Default config", func(t *testing.T) {
		resetMocks()
		const differentSiteAdminUID = 234
		mockAccessTokensCreate(t, differentSiteAdminUID, []string{authz.ScopeUserAll})
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) {
			return &types.User{ID: differentSiteAdminUID, SiteAdmin: true}, nil
		}
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()

		RunTests(t, []*Test{
			{
				Context: actor.WithActor(context.Background(), &actor.Actor{UID: differentSiteAdminUID}),
				Schema:  mustParseGraphQLSchema(t),
				Query: `
				mutation {
					createAccessToken(user: "` + uid1GQLID + `", scopes: ["user:all"], note: "n") {
						id
						token
					}
				}
			`,
				ExpectedResult: `null`,
				ExpectedErrors: []*gqlerrors.QueryError{
					{
						Path:          []interface{}{"createAccessToken"},
						Message:       "Must be authenticated as user with id 1",
						ResolverError: &backend.InsufficientAuthorizationError{Message: fmt.Sprintf("Must be authenticated as user with id %d", 1)},
					},
				},
			},
		})
	})

	t.Run("authenticated as different user who is a site-admin. Admin allowed", func(t *testing.T) {
		resetMocks()
		const differentSiteAdminUID = 234
		mockAccessTokensCreate(t, differentSiteAdminUID, []string{authz.ScopeUserAll})
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) {
			return &types.User{ID: differentSiteAdminUID, SiteAdmin: true}, nil
		}
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()

		conf.Get().AuthAccessTokens = &schema.AuthAccessTokens{Allow: string(conf.AccessTokensAdmin)}
		defer func() { conf.Get().AuthAccessTokens = nil }()

		RunTests(t, []*Test{
			{
				Context: actor.WithActor(context.Background(), &actor.Actor{UID: differentSiteAdminUID}),
				Schema:  mustParseGraphQLSchema(t),
				Query: `
				mutation {
					createAccessToken(user: "` + uid1GQLID + `", scopes: ["user:all"], note: "n") {
						id
						token
					}
				}
			`,
				ExpectedResult: `
				{
					"createAccessToken": {
						"id": "QWNjZXNzVG9rZW46MQ==",
						"token": "t"
					}
				}
			`,
			},
		})
	})

	t.Run("unauthenticated", func(t *testing.T) {
		resetMocks()
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) { return nil, database.ErrNoCurrentUser }
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()
		database.Mocks.Users.GetByID = func(_ context.Context, userID int32) (*types.User, error) {
			return &types.User{Username: "username"}, nil
		}
		defer func() { database.Mocks.Users.GetByID = nil }()

		ctx := actor.WithActor(context.Background(), nil)
		result, err := (&schemaResolver{db: db}).CreateAccessToken(ctx, &createAccessTokenInput{User: uid1GQLID, Note: "n"})
		if err == nil {
			t.Error("Expected error, but there was none")
		}
		if result != nil {
			t.Errorf("got result %v, want nil", result)
		}
	})

	t.Run("authenticated as different non-site-admin user", func(t *testing.T) {
		resetMocks()
		const differentNonSiteAdminUID = 456
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) { return &types.User{ID: differentNonSiteAdminUID}, nil }
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()
		database.Mocks.Users.GetByID = func(_ context.Context, userID int32) (*types.User, error) {
			return &types.User{Username: "username"}, nil
		}
		defer func() { database.Mocks.Users.GetByID = nil }()

		ctx := actor.WithActor(context.Background(), &actor.Actor{UID: differentNonSiteAdminUID})
		result, err := (&schemaResolver{db: db}).CreateAccessToken(ctx, &createAccessTokenInput{User: uid1GQLID, Note: "n"})
		if err == nil {
			t.Error("Expected error, but there was none")
		}
		if result != nil {
			t.Errorf("got result %v, want nil", result)
		}
	})

	t.Run("disable sudo token for dotcom", func(t *testing.T) {
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) {
			return &types.User{ID: 1, SiteAdmin: true}, nil
		}
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()

		conf.Get().AuthAccessTokens = &schema.AuthAccessTokens{Allow: string(conf.AccessTokensAdmin)}
		defer func() { conf.Get().AuthAccessTokens = nil }()

		envvar.MockSourcegraphDotComMode(true)
		defer envvar.MockSourcegraphDotComMode(false)

		ctx := actor.WithActor(context.Background(), &actor.Actor{UID: 1})
		_, err := (&schemaResolver{db: db}).CreateAccessToken(ctx, &createAccessTokenInput{
			User:   uid1GQLID,
			Scopes: []string{authz.ScopeUserAll, authz.ScopeSiteAdminSudo},
		})
		got := fmt.Sprintf("%v", err)
		want := "creation of access tokens with sudo scope is disabled"
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Mismatch (-want +got):\n%s", diff)
		}
	})
}

// 🚨 SECURITY: This tests that users can't delete tokens they shouldn't be allowed to delete.
func TestMutation_DeleteAccessToken(t *testing.T) {
	db := new(dbtesting.MockDB)

	mockAccessTokens := func(t *testing.T) {
		database.Mocks.AccessTokens.DeleteByID = func(id int64) error {
			if want := int64(1); id != want {
				t.Errorf("got %q, want %q", id, want)
			}
			return nil
		}
		database.Mocks.AccessTokens.GetByID = func(id int64) (*database.AccessToken, error) {
			if want := int64(1); id != want {
				t.Errorf("got %d, want %d", id, want)
			}
			return &database.AccessToken{ID: 1, SubjectUserID: 2}, nil
		}
	}

	token1GQLID := graphql.ID("QWNjZXNzVG9rZW46MQ==")

	t.Run("authenticated as user", func(t *testing.T) {
		resetMocks()
		mockAccessTokens(t)
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) {
			return &types.User{ID: 1, SiteAdmin: false}, nil
		}
		RunTests(t, []*Test{
			{
				Context: actor.WithActor(context.Background(), &actor.Actor{UID: 2}),
				Schema:  mustParseGraphQLSchema(t),
				Query: `
				mutation {
					deleteAccessToken(byID: "` + string(token1GQLID) + `") {
						alwaysNil
					}
				}
			`,
				ExpectedResult: `
				{
					"deleteAccessToken": {
						"alwaysNil": null
					}
				}
			`,
			},
		})
	})

	t.Run("authenticated as different user who is a site-admin", func(t *testing.T) {
		resetMocks()
		const differentSiteAdminUID = 234
		mockAccessTokens(t)
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) {
			return &types.User{ID: differentSiteAdminUID, SiteAdmin: true}, nil
		}
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()

		RunTests(t, []*Test{
			{
				Context: actor.WithActor(context.Background(), &actor.Actor{UID: differentSiteAdminUID}),
				Schema:  mustParseGraphQLSchema(t),
				Query: `
				mutation {
					deleteAccessToken(byID: "` + string(token1GQLID) + `") {
						alwaysNil
					}
				}
			`,
				ExpectedResult: `
				{
					"deleteAccessToken": {
						"alwaysNil": null
					}
				}
			`,
			},
		})
	})

	t.Run("unauthenticated", func(t *testing.T) {
		resetMocks()
		mockAccessTokens(t)
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) { return nil, database.ErrNoCurrentUser }
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()
		database.Mocks.Users.GetByID = func(_ context.Context, userID int32) (*types.User, error) {
			return &types.User{Username: "username"}, nil
		}
		defer func() { database.Mocks.Users.GetByID = nil }()

		ctx := actor.WithActor(context.Background(), nil)
		result, err := (&schemaResolver{db: db}).DeleteAccessToken(ctx, &deleteAccessTokenInput{ByID: &token1GQLID})
		if err == nil {
			t.Error("Expected error, but there was none")
		}
		if result != nil {
			t.Errorf("got result %v, want nil", result)
		}
	})

	t.Run("authenticated as different non-site-admin user", func(t *testing.T) {
		resetMocks()
		const differentNonSiteAdminUID = 456
		mockAccessTokens(t)
		database.Mocks.Users.GetByCurrentAuthUser = func(ctx context.Context) (*types.User, error) { return &types.User{ID: differentNonSiteAdminUID}, nil }
		defer func() { database.Mocks.Users.GetByCurrentAuthUser = nil }()
		database.Mocks.Users.GetByID = func(_ context.Context, userID int32) (*types.User, error) {
			return &types.User{Username: "username"}, nil
		}
		defer func() { database.Mocks.Users.GetByID = nil }()

		ctx := actor.WithActor(context.Background(), &actor.Actor{UID: differentNonSiteAdminUID})
		result, err := (&schemaResolver{db: db}).DeleteAccessToken(ctx, &deleteAccessTokenInput{ByID: &token1GQLID})
		if err == nil {
			t.Error("Expected error, but there was none")
		}
		if result != nil {
			t.Errorf("got result %v, want nil", result)
		}
	})
}
