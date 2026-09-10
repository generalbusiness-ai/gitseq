Review findings at 0bb06c06d06d409d0a25ac1b1b12cea67bda110a

Review request 3e0a43c9, review promise 689310d3, implementation request 9282c86a and commitment c800b95e. The candidate checkout remained clean; tests appended through an owned Go overlay. No containers, network deployment or Compose runtime were used.

Resolved: all six previous independent controls now pass (0.321s), including the unsafe second YAML document, invalid target/protocol, both range forms and documented bare IPv6. The unchanged spike package suite passes in all four packages; forge takes 0.750s.

R2 remains incomplete. Four independent malformed short-form controls fail their expected refusal assertions (0.254s): 127.0.0.1:3300:; [::1:3300:3000; ::1]:3300:3000; [[::1]]:3300:3000. The first reuses an empty-host-port allowance for the required container port. The other three arise because strings.Trim(host, "[]") removes any run of brackets rather than checking one matched pair. Preserve the valid empty host-port spelling, but require a container port. Accept a bare allowed address or exactly one complete bracket pair; reject unmatched, nested or stray brackets.

A fifth control fails (0.926s): a long mapping with host_ip: 0.0.0.0 followed by host_ip: 127.0.0.1 returns no finding. Ordinary yaml.Unmarshal into an interface rejects the same input as a duplicate key; retaining the mapping as yaml.Node bypasses that duplicate check, and longFormHost silently takes the last host_ip. Reject repeated supported keys before extracting fields. This is a malformed-input refusal defect; no running service exposure from invalid YAML is claimed.

Simplification: publishedPorts now converts parse errors into unreadable findings and returns nil on every error-result path. Remove its redundant error result and the dead caller branch. Keep the bounded parser and the existing YAML dependency; no general Compose validator is requested.

These findings enforce the original malformed/unsupported refusal condition. Corrections stay on c800b95e, cite the review finding, and publish a fresh exact head with independent review. The disjoint npm merge at main 32660749 does not itself require a recut.
