# Make resident readiness a positive check

Review for Hugh: use one readiness check in both startup guides that proves the expected workroom and spawned process answered. This is a small documentation and verification improvement using the existing identity endpoint.

The [deployment recipe](https://github.com/generalbusiness-ai/gitseq/blob/183211c3b5bff170171c5bfbe9bfbc3aacde08f6/docs/how-to/deploy-a-resident.md#L49) accepts a successful `gs status` exit as readiness and suppresses its diagnostic. Against a reserved, non-listening loopback socket, the documented plain status command exited 0 after local fallback. Its `if` branch would therefore accept readiness with no responding resident. The JSON control did the same. These were read-only controls; no live resident was stopped.

The [getting-started guide](https://github.com/generalbusiness-ai/gitseq/blob/183211c3b5bff170171c5bfbe9bfbc3aacde08f6/docs/getting-started.md#L188) already explains this behavior, but uses a different check: a live process and absence of one diagnostic string. The two recipes have drifted. The [CLI implementation](https://github.com/generalbusiness-ai/gitseq/blob/183211c3b5bff170171c5bfbe9bfbc3aacde08f6/cmd/gs/main.go#L2157) deliberately permits verified local reads; that useful behavior need not change.

Use a bounded positive check of the existing [identity endpoint](https://github.com/generalbusiness-ai/gitseq/blob/183211c3b5bff170171c5bfbe9bfbc3aacde08f6/internal/service/server.go#L155): require a successful response, the expected genesis, and the PID of the process just started. Keep checking that process is alive, and fail clearly on timeout. Identity proves which resident answered; it does not prove a new producer version or runtime is installed.

Exercise that same check with the correct resident, an absent resident, a different workroom, and an unrelated listener. The [documentation gate](https://github.com/generalbusiness-ai/gitseq/blob/183211c3b5bff170171c5bfbe9bfbc3aacde08f6/internal/docset/examples_test.go#L27) currently executes recipes with a free port; that successful path alone does not catch this false readiness result. Keep local status fallback and the resident ownership rules intact.

This recommendation is separate from binary provenance and adapter compatibility. It adds no workflow state or approval stage, and makes no measured efficiency claim. The approved request-authoring candidate 82f612df changes other parts of getting-started, but leaves these readiness recipes unchanged.
