// Test-only carrier for the real App pipeline. Never linked into tiana.
// Relative socket paths keep all artifacts in the test directory on platforms
// with short Unix socket name limits. The database engine/server are unchanged.
use app_sqlite::{EngineConfig, HttpConfig, PipelineService, serve};
use std::{path::PathBuf, sync::Arc, time::Duration};

fn main() {
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .enable_all()
        .build()
        .unwrap();
    runtime.block_on(async {
        let listener = tokio::net::UnixListener::bind("s").unwrap();
        let service = Arc::new(PipelineService::new(
            EngineConfig {
                database_path: PathBuf::from("app.db"),
                max_streams: 32,
                stream_ttl: Duration::from_millis(
                    std::env::var("TIANA_TEST_STREAM_TTL_MS")
                        .map(|v| v.parse().unwrap())
                        .unwrap_or(60_000),
                ),
                query_timeout: Duration::from_secs(2),
                busy_timeout: Duration::from_millis(100),
            },
            HttpConfig {
                max_request_bytes: 8 * 1024 * 1024,
                max_response_bytes: 8 * 1024 * 1024,
            },
        ));
        serve(listener, service, async {
            tokio::signal::ctrl_c().await.unwrap();
        })
        .await
        .unwrap();
    });
}
