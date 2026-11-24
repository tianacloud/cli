package apppublish

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/localstate"
	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/sdk-go/fetch"
)

// PublicationReceipt contains recovery identity only; no credentials or payload.
type PublicationReceipt struct {
	Origin, TenantID, UserID, WebID, InstanceID, PublishID, SHA256, ArchivePath string
	CreatedAt                                                                   time.Time
}

type serverlessSession struct {
	bound     Runner
	project   WebProject
	instance  authclient.Instance
	transport FetchTransport
}

func (r Runner) openServerless(ctx context.Context, id, notReadyMessage string) (*serverlessSession, *Error) {
	network, networkErr := netconfig.Policy(ctx)
	if networkErr != nil {
		return nil, inputError(networkErr.Error())
	}
	bound, e := r.Bind(ctx)
	if e != nil {
		return nil, e
	}
	project, e := bound.Resolve(ctx, id)
	if e != nil {
		return nil, e
	}
	if project.ID != id {
		project, e = bound.projectByID(ctx, project.ID)
		if e != nil {
			return nil, e
		}
	}
	if project.InstanceID == "" {
		return nil, inputError("Web has no instance binding")
	}
	instance, err := r.Client.GetInstance(ctx, project.InstanceID)
	if err != nil || instance.ID != project.InstanceID || instance.Engine != "web" || instance.ProductState != "ACTIVE" || instance.DeletionPending || instance.Connection == nil {
		return nil, inputError(notReadyMessage)
	}
	origin, err := url.Parse(instance.Connection.URL)
	if err != nil || origin.Scheme != "https" || origin.Hostname() != instance.Connection.Hostname || origin.Path != "" || origin.RawQuery != "" || origin.User != nil {
		return nil, inputError("Invalid Web Gateway connection")
	}
	// Metadata is account-scoped; freeze the identity before opening the data path.
	if _, e := bound.Bind(ctx); e != nil {
		return nil, e
	}
	credential, err := r.Client.ConnectionCredential(ctx, bound.PrincipalID, bound.TenantID)
	if err != nil {
		return nil, &Error{Code: "WEB_CREDENTIAL_UNAVAILABLE", Message: "Cannot resolve Web data credential", NextAction: "Check TIANA_TOKEN/TIANA_TOKEN_FILE or the current account login", ExitCode: 5}
	}
	defer clear(credential)
	config := fetch.Config{Endpoint: instance.Connection.Hostname, Token: string(credential), DialAddress: os.Getenv("TIANA_GATEWAY_ADDRESS"), Network: network}
	// Proxy selection uses the logical port even with a physical dial override.
	if origin.Port() != "" && origin.Port() != "443" {
		config.Port = origin.Port()
	}
	if trust := clientconfig.FromContext(ctx); trust != nil {
		config.RootCAs = trust.Roots
	}
	var transport FetchTransport
	if r.Fetch != nil {
		transport, err = r.Fetch(config)
	} else {
		transport, err = fetch.NewClient(config)
	}
	if err != nil {
		return nil, inputError("Cannot initialize Web Gateway transport")
	}
	return &serverlessSession{bound: bound, project: project, instance: instance, transport: transport}, nil
}

func (r Runner) runServerless(ctx context.Context, o Options) Result {
	session, e := r.openServerless(ctx, o.ID, "Web instance is not ready for publication")
	if e != nil {
		return Failure(e)
	}
	defer session.transport.Close()
	bound := session.bound
	project := session.project
	instance := session.instance
	transport := session.transport
	request := fetch.Request{Method: "GET", PathQuery: "/_tiana/web/current", BodyLength: 0}
	publishID := ""
	digest := ""
	archivePath := ""
	retainArchive := false
	if o.Command == "publish" {
		archive, sha, e := PackFile(o.Dir)
		if e != nil {
			return Failure(inputError(e.Error()))
		}
		if e = ValidateArchiveEntry(archive, project.Entry); e != nil {
			archive.Close()
			os.Remove(archive.Name())
			return Failure(inputError(e.Error()))
		}
		archivePath = archive.Name()
		defer func() {
			archive.Close()
			if !retainArchive {
				os.Remove(archivePath)
			}
		}()
		stat, e := archive.Stat()
		if e != nil {
			return Failure(inputError("Cannot inspect packaged archive"))
		}
		var id [16]byte
		if _, e = rand.Read(id[:]); e != nil {
			return Failure(inputError("Cannot generate publication identity"))
		}
		publishID = hex.EncodeToString(id[:])
		digest = sha
		request = fetch.Request{Method: "PUT", PathQuery: "/_tiana/web/publish", Body: archive, BodyLength: stat.Size(), Headers: []fetch.Header{{Name: "x-tiana-publish-id", Value: publishID}, {Name: "x-tiana-content-sha256", Value: sha}}}
		if r.PersistPublication != nil {
			if err := r.PersistPublication(ctx, PublicationReceipt{Origin: r.Client.Origin(), TenantID: bound.TenantID, UserID: bound.PrincipalID, WebID: project.ID, InstanceID: instance.ID, PublishID: publishID, SHA256: digest, ArchivePath: archivePath, CreatedAt: time.Now().UTC()}); err != nil {
				failure := &Error{Code: "PUBLISH_RECEIPT_UNAVAILABLE", Message: "Cannot safely save publication identity; no upload was sent", NextAction: "Check the local receipt directory permissions", ExitCode: 1}
				var local *localstate.Error
				if errors.As(err, &local) {
					failure.Message += ": " + local.Error()
					failure.NextAction = "Set XDG_CONFIG_HOME to a writable directory or repair the local receipt path"
					if local.Code == "LEGACY_PENDING_COMMAND" {
						failure.NextAction = "Set TIANA_PENDING_COMMAND_FILE to the previous path and recover the original command"
					}
					if local.Code == "LOCAL_STATE_BUSY" {
						failure.NextAction = "Wait for the other CLI command to release the receipt lock"
					}
				}
				return Failure(failure)
			}
		}
		if r.OnPublishID != nil {
			r.OnPublishID(publishID)
		}
		retainArchive = true
	} else if o.PublishID != "" {
		publishID = o.PublishID
		request.PathQuery = "/_tiana/web/publish/" + url.PathEscape(o.PublishID)
	}
	result, err := transport.Do(ctx, request)
	if err != nil {
		code := 1
		message := "Web status query failed"
		if o.Command == "publish" {
			code = 4
			message = "Publication outcome unknown; the request is not automatically replayed"
		}
		return Result{Status: map[bool]string{true: "unknown", false: "failed"}[code == 4], Data: map[string]string{"id": project.ID, "publish_id": publishID, "sha256": digest, "archive_path": archivePath}, Error: &Error{Code: "WEB_GATEWAY_FAILED", Message: message, NextAction: "Query tiana web status " + project.ID + " --publish-id " + publishID, ExitCode: code}}
	}
	defer result.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(result.Body, 65537))
	if err != nil || len(raw) > 65536 {
		if o.Command == "publish" {
			return Result{Status: "unknown", Data: map[string]string{"id": project.ID, "publish_id": publishID, "sha256": digest, "archive_path": archivePath}, Error: &Error{Code: "INVALID_UPLOAD_RECEIPT", Message: "Publication response incomplete; outcome unknown", NextAction: "Query publication status before another publish", ExitCode: 4}}
		}
		return Failure(inputError("Invalid Web control response"))
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil || body == nil {
		if o.Command == "publish" {
			return Result{Status: "unknown", Data: map[string]string{"id": project.ID, "publish_id": publishID, "sha256": digest, "archive_path": archivePath}, Error: &Error{Code: "INVALID_UPLOAD_RECEIPT", Message: "Publication response invalid; outcome unknown", NextAction: "Query publication status before another publish", ExitCode: 4}}
		}
		return Failure(inputError("Invalid Web control JSON"))
	}
	if o.Command == "status" {
		body["query_kind"] = "current"
		if o.PublishID != "" {
			body["query_kind"] = "receipt"
		}
	}
	if result.Status >= 300 {
		if o.Command == "publish" {
			body["archive_path"] = archivePath
		}
		exit := 1
		code := "WEB_CONTROL_FAILED"
		if errorBody, ok := body["error"].(map[string]any); ok {
			if name, ok := errorBody["code"].(string); ok {
				code = name
			}
		}
		if o.Command == "status" && result.Status == 404 && code == "PUBLISH_NOT_FOUND" {
			return Result{Status: "unknown", Data: map[string]string{"id": project.ID, "publish_id": publishID, "query_kind": "receipt"}, Error: &Error{Code: code, Message: "Publication receipt unavailable after expiry or App restart; outcome unknown", NextAction: "Query tiana web status " + project.ID + " and compare current checksums", ExitCode: 4}}
		}
		if o.Command == "publish" && result.Status >= 500 {
			exit = 4
		}
		return Result{Status: map[bool]string{true: "unknown", false: "failed"}[exit == 4], Data: body, Error: &Error{Code: code, Message: "Web control rejected the request", NextAction: "Query publication status before submitting a new publish", ExitCode: exit}}
	}
	if o.Command == "publish" {
		if body["publish_id"] != publishID || body["sha256"] != digest || body["uploaded"] != true {
			return Result{Status: "unknown", Data: map[string]string{"id": project.ID, "publish_id": publishID, "sha256": digest, "archive_path": archivePath}, Error: &Error{Code: "INVALID_UPLOAD_RECEIPT", Message: "S3 upload receipt is inconsistent", NextAction: "Query publication status; do not replay upload", ExitCode: 4}}
		}
	}
	if o.Command == "publish" {
		retainArchive = false
	}
	body["endpoint"] = project.Endpoint
	if o.Command == "publish" {
		if failure := bound.notifyAppVersion(ctx, project.ID); failure != nil {
			return Result{Status: "failed", Data: body, Error: failure}
		}
	}
	body["console_url"] = project.ConsoleURL
	body["mini_program_url"] = project.MiniProgramURL
	body["mini_program_code_url"] = project.MiniProgramCodeURL
	return Success(body)
}

type FetchTransport interface {
	Do(context.Context, fetch.Request) (*fetch.Response, error)
	Close()
}

// Retry only the harmless invalidation signal, never the upload. A lost response
// may advance twice; generations are not release counts.
func (r Runner) notifyAppVersion(ctx context.Context, id string) *Error {
	for attempt := 0; attempt < 3; attempt++ {
		result, failure := r.boundRequest(ctx, "POST", "/api/v1/web-projects/"+url.PathEscape(id)+"/version", nil)
		if failure == nil && result.Status == 204 {
			return nil
		}
		if ctx.Err() != nil || (failure != nil && failure.HTTPStatus > 0 && failure.HTTPStatus < 500 && failure.HTTPStatus != 429) {
			break
		}
	}
	return &Error{Code: "WEB_VERSION_NOTIFICATION_FAILED", Message: "Application uploaded, but cache update notification failed", NextAction: "Retry the empty POST /api/v1/web-projects/" + id + "/version notification; do not upload again", ExitCode: 1}
}
