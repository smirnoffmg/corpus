# Third-party notices

The code of corpus is under the MIT license (see `LICENSE`). Some files in this
repository, and some packages the build pulls in, are under licenses of their
own.

## Files in this repository

| Files | Source | License |
| --- | --- | --- |
| `front/src/csl/apa.csl`, `front/src/csl/ieee.csl` | [Citation Style Language styles](https://github.com/citation-style-language/styles) | [CC BY-SA 3.0](https://creativecommons.org/licenses/by-sa/3.0/) |
| `front/src/csl/locales-en-US.xml`, `front/src/csl/locales-ru-RU.xml` | [Citation Style Language locales](https://github.com/citation-style-language/locales) | [CC BY-SA 3.0](https://creativecommons.org/licenses/by-sa/3.0/) |
| `front/src/csl/gost-r-7-0-100-2018.csl` | written for corpus, in the CSL format | [CC BY-SA 3.0](https://creativecommons.org/licenses/by-sa/3.0/), as CSL styles conventionally are |

The CC BY-SA license covers those files only; changed copies of them have to
stay under it. It does not extend to the rest of the code.

The HTML test fixtures in `back/internal/extract/testdata` imitate the markup
of Sphinx and docutils pages; their text was written for the tests.

## Packages the build installs

These are not in the repository; Go modules and npm fetch them. The web UI
image built from `front/` bundles the second group.

- **Go modules** — pgx, goose, the MCP Go SDK, testify, testcontainers-go,
  `golang.org/x/net` and `golang.org/x/sync`: MIT, BSD or Apache 2.0.
  `cd back && go list -m all` lists them.
- **[citeproc-js](https://github.com/Juris-M/citeproc-js)** © 2009–2019 Frank
  Bennett, under CPAL-1.0 or AGPL-3.0, formats citations in the UI. The UI
  names it where it is used, as CPAL's attribution requires. Its source is
  available from the project, unmodified.
- **React, React Router** — MIT.
- **PT Sans, PT Serif, PT Mono** (ParaType, via @fontsource) — SIL Open Font
  License 1.1.

`cd front && npm ls --all` lists every package with its version.
