# Third-party material in the console

Packages are listed in ADR 0012. This file covers material copied into the
source tree rather than installed.

## Lucide icons (`src/components/Icon.tsx`)

SVG path data for the icons `layers`, `check`, `triangle-alert`,
`circle-x`, `loader-circle`, `circle-alert`, `external-link` and `plus`,
from Lucide (https://lucide.dev).

ISC License

Copyright (c) for portions of Lucide are held by Cole Bemis 2013-2022 as
part of Feather (MIT). All other copyright (c) for Lucide are held by
Lucide Contributors 2022.

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY
SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR
IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

## Design rule text (`design-rules/`)

Reviewed markdown from Impeccable (Apache-2.0), Anthropic's skills
repository (Apache-2.0), vibecoded-design-tells (MIT) and Vercel's Web
Interface Guidelines (MIT). Sources, commits and licences are in
`design-rules/README.md`. These are reference documents, not shipped in
the build.

## Fonts

IBM Plex Sans and IBM Plex Mono (SIL Open Font License 1.1), bundled from
`@fontsource/ibm-plex-sans` and `@fontsource/ibm-plex-mono`.
