//! GPUI `HttpClient` backed by a small pool of blocking `ureq` workers, so
//! `img("http://127.0.0.1:8080/assets/image/…")` loads and caches card art.

use futures::channel::oneshot;
use futures::future::BoxFuture;
use http_client::http::{HeaderValue, Request, Response};
use http_client::{AsyncBody, HttpClient, Url};
use std::sync::mpsc::{channel, Sender};
use std::sync::{Arc, Mutex};
use std::time::Duration;

type Job = (String, oneshot::Sender<anyhow::Result<(u16, Vec<u8>)>>);

pub struct PoolClient {
    jobs: Mutex<Sender<Job>>,
}

impl PoolClient {
    pub fn new(workers: usize) -> Arc<Self> {
        let (sender, receiver) = channel::<Job>();
        let receiver = Arc::new(Mutex::new(receiver));
        let agent = ureq::AgentBuilder::new().timeout_connect(Duration::from_secs(2)).timeout(Duration::from_secs(30)).build();
        for _ in 0..workers {
            let receiver = receiver.clone();
            let agent = agent.clone();
            std::thread::Builder::new().name("image-fetch".into()).spawn(move || loop {
                let job = receiver.lock().unwrap().recv();
                let Ok((url, reply)) = job else { return };
                let result = (|| -> anyhow::Result<(u16, Vec<u8>)> {
                    let response = match agent.get(&url).call() {
                        Ok(response) => response,
                        Err(ureq::Error::Status(_, response)) => response,
                        Err(error) => return Err(error.into()),
                    };
                    let status = response.status();
                    let mut bytes = Vec::new();
                    std::io::Read::read_to_end(&mut response.into_reader(), &mut bytes)?;
                    Ok((status, bytes))
                })();
                let _ = reply.send(result);
            }).expect("spawn image worker");
        }
        Arc::new(PoolClient { jobs: Mutex::new(sender) })
    }
}

impl HttpClient for PoolClient {
    fn user_agent(&self) -> Option<&HeaderValue> { None }
    fn proxy(&self) -> Option<&Url> { None }
    fn send(&self, request: Request<AsyncBody>) -> BoxFuture<'static, anyhow::Result<Response<AsyncBody>>> {
        let (reply, receive) = oneshot::channel();
        let sent = self.jobs.lock().unwrap().send((request.uri().to_string(), reply));
        Box::pin(async move {
            sent.map_err(|_| anyhow::anyhow!("image pool stopped"))?;
            let (status, bytes) = receive.await??;
            Ok(Response::builder().status(status).body(AsyncBody::from(bytes))?)
        })
    }
}
