# Third-party notices

The embedded GNU Unifont 16.0.04 subset is copied pixel-for-pixel from the C1ancher package manager. Its SIL OFL 1.1 license and copyright notices are in font-LICENSE.txt (source: assets/font-LICENSE.txt). No user books are distributed.

The archive includes the application and c1device sources, font inputs and generation/build scripts, and the original upstream dependency archives. The reader's current pinned modules are golang.org/x/image v0.45.0, golang.org/x/net v0.59.0, golang.org/x/text v0.42.0 and golang.org/x/sys v0.48.0; run go mod download before an offline build because the historical third-party/goproxy material does not contain all current versions. The Go toolchain itself is not bundled. First-party code is GPL-3.0-only, with its complete license in LICENSE; fonts and dependencies retain their separate terms.

The internal/mobiformat format algorithms are adapted from KindleUnpack, revision bf0ca6e (https://github.com/kevinhendricks/KindleUnpack), under GPL v3. Copyright 2009 Charles M. Hannum; extensions 2009-2020 P. Durrant, K. Hendricks, S. Siebert, fandrieu, DiapDealer, nickredding, and tkeo. The Go implementation uses bounded records and disk spans instead of whole-book buffers. The complete GPL v3 text is in the application LICENSE. No DRM removal code or third-party sample books are included.

Go runtime and golang.org/x/image, golang.org/x/net, golang.org/x/text, golang.org/x/sys:

Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
