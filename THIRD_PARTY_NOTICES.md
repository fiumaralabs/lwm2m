# Third-party notices

This project is licensed under the Apache License 2.0 (see `LICENSE`).
The test vectors in `spec/vectors/` contain data extracted from the test suites of the projects below, used under their licences.
Each vector's `source` field cites the originating file and line.

## Eclipse Leshan
- https://github.com/eclipse-leshan/leshan, commit `22bc753133829b91aa1f090e1e1179e6957940a0`
- Dual-licensed EPL-2.0 or BSD-3-Clause (Eclipse Distribution License 1.0). Used under BSD-3-Clause.
- Copyright (c) 2007, Eclipse Foundation, Inc. and its licensors. All rights reserved.

> Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:
> Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
> Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
> Neither the name of the Eclipse Foundation, Inc. nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.
>
> THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

## Eclipse Wakaama
- https://github.com/eclipse-wakaama/wakaama, commit `94ff56f77a2d24a5890e0e703809a47633aa7d4b`
- Dual-licensed EPL-2.0 or BSD-3-Clause (Eclipse Distribution License 1.0). Used under BSD-3-Clause, same terms as above.
- Copyright (c) 2007, Eclipse Foundation, Inc. and its licensors. All rights reserved.

## Zephyr Project
- https://github.com/zephyrproject-rtos/zephyr, commit `74b7173e9c929cf8eed570fb3df099c5514720c0`, `tests/net/lib/lwm2m/`
- Licensed under the Apache License 2.0.
- Copyright (c) the Zephyr Project contributors.

## OMA SpecWorks LwM2M specifications
- `spec/vectors/spec-examples.json` reproduces short worked examples from OMA-TS-LightweightM2M_Core / _Transport V1_2_2-20240613-A, each cited by section, for interoperability testing.
- The requirement tables in `spec/` paraphrase the OMA specifications with section references. The specifications themselves are available from https://www.openmobilealliance.org/release/LightweightM2M/.
- LwM2M and OMA SpecWorks are trademarks of their respective owners. This project is not affiliated with or endorsed by OMA SpecWorks.

## OMNA LwM2M Registry (OMA object definitions)
- https://github.com/OpenMobileAlliance/lwm2m-registry, commit `7d5204dde519bc3f9ae6435d5e8d62e25da4a8dd`
- `model/registry/` holds unmodified copies of `version_history/<id>-<maj>_<min>.xml` for objects 0-28, embedded into the `model` package. Each file keeps its original licence header.
- Licensed under the OMA BSD-3-Clause licence (`License/OMA-License.txt` in the registry). Copyright 2017-2026 Open Mobile Alliance.

> Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:
> 1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
> 2. Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
> 3. Neither the name of the copyright holder nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.
>
> THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
>
> The above license is used as a license under copyright only. Please reference the OMA IPR Policy for patent licensing terms: https://www.omaspecworks.org/about/intellectual-property-rights/

## Pion DTLS
- https://github.com/pion/dtls (MIT License, copyright (c) the Pion community <https://pion.ly>). Not vendored: the module depends on `github.com/fiumaralabs/dtls/v3`, a fork of pion/dtls v3 with LwM2M patches (RFC 7250 raw public keys and alert/session/curve/port-reuse fixes) on the `lwm2m-v3` branch, documented in its `LWM2M.md`. The fork keeps pion's MIT licence.

## go-coap
- https://github.com/plgd-dev/go-coap, v3.5.4 (Apache License 2.0, copyright (c) the go-coap authors). `internal/dtlscoap` adapts go-coap's `net.DTLSListener` and `dtls.Client` to the pion/dtls fork.
