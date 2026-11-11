//! Helpers to run blocking API calls off the UI thread and apply the result.

use crate::api::Api;
use gpui_kit::*;

/// Runs `work` on the background executor, then `done` on the view.
pub fn spawn_api<V: 'static, T: Send + 'static>(
    cx: &mut Context<V>,
    api: &Api,
    work: impl FnOnce(&Api) -> anyhow::Result<T> + Send + 'static,
    done: impl FnOnce(&mut V, anyhow::Result<T>, &mut Context<V>) + 'static,
) {
    let api = api.clone();
    let background = cx.background_executor().clone();
    cx.spawn(async move |this, cx| {
        let result = background.spawn(async move { work(&api) }).await;
        let _ = this.update(cx, |view, cx| {
            done(view, result, cx);
            cx.notify();
        });
    })
    .detach();
}

/// Same as [`spawn_api`] with access to the window (to update inputs).
pub fn spawn_api_in<V: 'static, T: Send + 'static>(
    window: &mut Window,
    cx: &mut Context<V>,
    api: &Api,
    work: impl FnOnce(&Api) -> anyhow::Result<T> + Send + 'static,
    done: impl FnOnce(&mut V, anyhow::Result<T>, &mut Window, &mut Context<V>) + 'static,
) {
    let api = api.clone();
    let background = cx.background_executor().clone();
    cx.spawn_in(window, async move |this, cx| {
        let result = background.spawn(async move { work(&api) }).await;
        let _ = this.update_in(cx, |view, window, cx| {
            done(view, result, window, cx);
            cx.notify();
        });
    })
    .detach();
}

/// Calls `tick` on the view after `delay`, used for debounced searches.
pub fn after<V: 'static>(cx: &mut Context<V>, delay: std::time::Duration, tick: impl FnOnce(&mut V, &mut Context<V>) + 'static) -> Task<()> {
    let background = cx.background_executor().clone();
    cx.spawn(async move |this, cx| {
        background.timer(delay).await;
        let _ = this.update(cx, |view, cx| tick(view, cx));
    })
}
