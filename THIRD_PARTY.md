# Acknowledgments, sources and licensing notes

Reviewed November 25, 2026. Quetzal benefits from earlier revival projects and
public research. Credit belongs to their authors and contributors. This file
records source consultation, known internal reuse and development dependencies;
it is not a declaration that every implementation was developed independently.

## PSHome-MultiServer

Thank you to **GitHubProUser67 and the PSHome-MultiServer contributors** for
publishing their Quazal and Eden/edNet work. Their implementation was explicitly
consulted online during Quetzal's TDU research, rather than being an accidental
similarity or a reference inferred after implementation.

The recorded revision is
[`8778e985e4fbbec9ee8f29d007d256dd1df5814f`](https://github.com/GitHubProUser67/PSHome-MultiServer/tree/8778e985e4fbbec9ee8f29d007d256dd1df5814f).
Consulted areas include the Lair bootstrap method, native master/proxy and
challenge-store/statistics service and model definitions. These supplied guidance
and comparison points alongside local PS2 executable analysis and native-routine
validation. See the [TDU source inventory](docs/games/tdu/external-references.md)
for exact paths and implementation areas to compare.

The [LICENSE at that revision](https://github.com/GitHubProUser67/PSHome-MultiServer/blob/8778e985e4fbbec9ee8f29d007d256dd1df5814f/LICENSE)
contains **GNU GPL version 3**. Its license blob is
`f288702d2fa16d3cdf0035b15a9fcbc552cd88e7`, verified through GitHub's contents API.
Acknowledgment alone does not satisfy licensing obligations for copied or
adapted GPL-covered implementation. If such reuse is identified, the applicable
notice, licensing and corresponding-source requirements must be addressed;
rewriting code in Go does not by itself establish independent authorship.
See [GPLv3 sections 4–6](https://www.gnu.org/licenses/gpl-3.0.en.html).

This provenance review establishes consultation. It does **not** certify that
no implementation logic was translated or adapted, nor does it establish that
all of Quetzal is a derivative of that project. The earlier unqualified claim
that no implementation was copied has been withdrawn pending specific code
comparisons. Shared protocol names, field layouts and constants alone do not
resolve that question. In particular, `edNet` names also occur in the original
TDU PS2 executable; they are not unique to PSHome-MultiServer.

## 25tl-rdv and original game documentation

Thank you to **michal-kapala and the 25tl-rdv contributors** for the original
statistics-FAQ lead and protocol documentation used when researching 25 to Life
rankings, personal-stat rank fields and localized messages. The recorded
revision is
[`6d10c7469ea12c1028a44e14fceb1fb27c88c5c3`](https://github.com/michal-kapala/25tl-rdv/tree/6d10c7469ea12c1028a44e14fceb1fb27c88c5c3).
Its [LICENSE](https://github.com/michal-kapala/25tl-rdv/blob/6d10c7469ea12c1028a44e14fceb1fb27c88c5c3/LICENSE)
contains **MIT plus Commons Clause v1.0**, not plain MIT. The additional condition
restricts selling the covered software and defined related services. The checked
license blob is `b03ac5d86abf80c4613fd9f8d0411a38fe2fcb2a`.
See [rankings and source details](docs/games/25tolife/rankings.md).

The original [25 to Life statistics FAQ](https://web.archive.org/web/20071223122600/http://stats.25tolife.com/faq/)
was used for published rating/scoring rules. Publisher-hosted
[25 to Life](https://support.na.square-enix.com/document/manual/982/25_to_Life_PS2.pdf)
and [Project: Snowblind](https://support.na.square-enix.com/document/manual/1008/Project_Snowblind_PS2.pdf)
manuals were consulted for player-facing behavior. The Snowblind GameFAQs index
was also checked; it did not establish an original scoring formula. These are
research sources, not permission to redistribute their documents or game assets.

## Confirmed reuse within the author's projects

| Source | Use in Quetzal | Licensing/provenance note |
| --- | --- | --- |
| Sibling `tdu-server` workspace | Shared PRUDP, Quazal account/ticket/crypto code and TDU implementation were adapted and migrated into Quetzal. Source headers identify several migrated files. | This is actual implementation reuse. That workspace is a research predecessor, not an external runtime dependency; its own external references still matter. |
| [Angelware](https://github.com/dead-game-entrepreneurs/angelware) | Repository/workflow patterns and SNR2 research helpers, including `tools/snr2.py` and `ghidra/scripts/ApplySnr2Symbols.java`. | The helper headers explicitly identify adaptation. No project-wide license file was found in the inspected local Angelware checkout; do not infer an MIT or other grant from public availability. |

These origins do not erase the provenance of any external material consulted or
incorporated earlier in those projects.

## Runtime and research tools

`go.mod` declares no third-party modules. A dependency-graph check of
`./cmd/quetzal` found only this module and Go's standard library. This establishes
the import graph; it cannot detect source copied into the repository.

| Component | Role | License/source notes |
| --- | --- | --- |
| [Go](https://go.dev/) | Compiler, runtime and standard library; standard-library code is linked into the server. | The pinned Go 1.27.4 toolchain's BSD-style notice is retained in [third_party/licenses/Go-LICENSE.txt](third_party/licenses/Go-LICENSE.txt) and copied into the server image at `/licenses/Go-LICENSE.txt`. |
| [Ghidra](https://github.com/NationalSecurityAgency/ghidra) and [ghidra-emotionengine-reloaded](https://github.com/chaoticgd/ghidra-emotionengine-reloaded) | Local disassembly/decompilation and PS2 Emotion Engine support. | Their upstream root LICENSE files contain Apache License 2.0; consult each distribution for bundled-component notices. They are not included in the server image. |
| [Unicorn](https://github.com/unicorn-engine/unicorn) | Native-routine validation, imported by research scripts. | Consult upstream [COPYING](https://github.com/unicorn-engine/unicorn/blob/master/COPYING) and its component/binding notices; the core and bindings have distinct notices. Not linked into the Go server or bundled in its image. |
| [Capstone](https://github.com/capstone-engine/capstone) | Local instruction inspection during native-harness debugging. | Used in the private research environment; consult the installed version's license notices. No Capstone dependency is declared by the Go server. |

Docker/Compose, Python, disc-extraction utilities and the host emulator are
operator-provided tools. They retain their own distribution licenses. Using a
tool does not identify it as the author of Quetzal's protocol implementation.

## Audit scope and remaining work

This review checked tracked source headers, imports, dependency manifests,
research documentation, Git history and available local research/tool-call
records. Those records contain explicit GitHub/web access to the references
above. They also include work on other revival repositories; references used
only for those unrelated projects were not attributed to Quetzal.

A focused comparison of the recorded TDU master/bootstrap/proxy reference files
found shared protocol subject matter and implementation differences. For
example, Quetzal's PS2 master codec treats the second reply word as a directory
IPv4, whereas the pinned reference master names it an encryption-key setting.
That comparison is not a complete similarity or translated-code audit.

No additional external Go module or explicitly identified third-party runtime
source import was found in this pass. That is not proof that every similar
function has independent provenance. A report of matching code should include
the Quetzal path, upstream path/revision and relevant section so it can be
reviewed and its notices/license treatment corrected where needed.

Quetzal currently has no project-wide LICENSE file. This acknowledgment does
not choose one, relicense upstream work, grant an exception, or imply permission
from any upstream maintainer. Applicable third-party notices remain in effect.
