import { Component, type ErrorInfo, type ReactNode } from "react";

// React 19 unmounts the entire tree when a render throws and nothing catches
// it, and a reload replays the same durable records — so a single malformed
// one takes the whole page down permanently rather than for one paint. The
// boundary turns that into a contained, readable failure: the rest of the
// session stays usable, and the message says what to do.
//
// It deliberately does not retry by itself. The projection is replayed from
// the log, so an automatic retry would loop on the same record.
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("gitseq: a render failed and was contained by the error boundary", error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div role="alert" className="m-4 rounded-md border border-danger p-4 text-sm">
        <p className="font-medium">Something in this workroom could not be rendered.</p>
        <p className="mt-1 text-faint">
          The rest of the log is unaffected and nothing has been changed. This is a display failure, not a
          durable one.
        </p>
        <pre className="mt-2 overflow-x-auto font-mono text-[11px] text-faint">{this.state.error.message}</pre>
        <button
          type="button"
          className="mt-3 rounded border border-border px-2 py-1 text-xs"
          onClick={() => this.setState({ error: null })}
        >
          Try rendering again
        </button>
      </div>
    );
  }
}
