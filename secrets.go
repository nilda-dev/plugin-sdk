package nilda

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Secrets is a plugin's own place to keep a credential with Core (`secrets`). Core stores each value encrypted,
// returns it to this plugin alone, and still has it after a restart — which the plugin's kv does not promise on
// every install: on a Lite install kv lives in Core's memory.
//
// It is for what the OWNER handed the plugin for another service, and what that service handed back: an OAuth
// refresh token that rotates on every use, a webhook signing secret shown once. A setting the owner types on the
// plugin's admin page (`secret: true`) needs none of this — Core already keeps those, and delivers them at Init.
//
// Every call is HTTP under /api/rest/v1/plugin-secrets with this plugin's token, and Core takes the plugin's
// identity from the token, never from the request, so no call can name another plugin's secret. Another plugin's
// name answers "not found" exactly as an unset one does.
//
// A name is 1–64 lowercase letters, digits or underscores; a value 1–4096 bytes; a plugin keeps at most 32 names.
// Replacing a name you already keep is always allowed, so a plugin at the limit can still rotate its token.
// An uninstall removes them all.
//
// Nil-safe like the rest of the client: on a plugin with no API access every call returns the client's own error.
// A refusal is an *APIError: 403 for a manifest that does not declare `secrets`, 422 for a bad name or value.
type Secrets struct{ api *API }

// Secrets returns the secret calls.
func (a *API) Secrets() *Secrets { return &Secrets{api: a} }

// Set keeps value under name, replacing the value there.
func (s *Secrets) Set(ctx context.Context, name, value string) error {
	if err := validSecret(name, value); err != nil {
		return err
	}
	return s.api.JSON(ctx, http.MethodPut, "/plugin-secrets/"+name, secretBody{Value: value}, nil)
}

// Get reads a secret back; found is false when none is kept under name.
func (s *Secrets) Get(ctx context.Context, name string) (value string, found bool, err error) {
	if !secretName.MatchString(name) {
		return "", false, badSecretName(name)
	}
	var out struct {
		Data secretBody `json:"data"`
	}
	err = s.api.Get(ctx, "/plugin-secrets/"+name, nil, &out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.NotFound() {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return out.Data.Value, true, nil
}

// Delete removes a secret and reports whether there was one to remove. Disconnecting an account is one call here
// plus whatever revoking the token at its provider takes; a second Delete finds nothing and is not an error.
func (s *Secrets) Delete(ctx context.Context, name string) (removed bool, err error) {
	if !secretName.MatchString(name) {
		return false, badSecretName(name)
	}
	err = s.api.JSON(ctx, http.MethodDelete, "/plugin-secrets/"+name, nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.NotFound() {
		return false, nil
	}
	return err == nil, err
}

func badSecretName(name string) error {
	return fmt.Errorf("nilda: a secret's name is 1–64 lowercase letters, digits or underscores, got %q", name)
}
