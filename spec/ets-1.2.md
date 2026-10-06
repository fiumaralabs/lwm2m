# OMA ETS LwM2M INT 1.2 (and 1.2.1-C): test inventory for our Server + Bootstrap-Server

Compiled 2026-10-06 from the text of both PDFs, downloaded from `https://www.openmobilealliance.org/release/LightweightM2M/ETS/` and extracted with `pdftotext` (plain and `-layout`). Local copies: `scratchpad/specs/OMA-ETS-LightweightM2M_INT-V1_2-20231003-A.{pdf,txt}` and `...V1_2_1-20240312-C.{pdf,txt}`. Every procedure, value and pass criterion below paraphrases that text. Where the ETS text is wrong or self-contradictory, it is flagged **[ETS erratum]** and the wording is kept.

Related files: requirement IDs come from `spec/standards.md` §3. Zephyr automation comes from `spec/zephyr-interop.md` §2.

---

## 1. Document info

| Item | 1.2 (approved) | 1.2.1 (candidate) |
|---|---|---|
| File | `ETS/OMA-ETS-LightweightM2M_INT-V1_2-20231003-A.pdf` (183 pages) | `ETS/OMA-ETS-LightweightM2M_INT-V1_2_1-20240312-C.pdf` |
| Status line | "Approved Version: 1.2 - 2023-10-03", build `main: 17 Oct 2023 rev db372f8` | "Candidate Version: 1.2.1 - 2024-03-12", build `main: 03 Apr 2024 rev 13240ce` |
| History (App. A) | 26 Sep 2017 1.0 approved by TP; 15 Aug 2018 approved by DM; 03 Oct 2023 approved by DMSO, ratified by OMA BoD 16 Oct 2023 | adds "12 Mar 2024 Document Approved by DMSO" (still labelled Candidate) |
| Scope (§1) | Says it covers "OMA-TS-LightweightM2M-V1_1-20180710-A and ..._Transport-V1_1-20180710-A". §4 still says "Enabler Release V1.0". Both statements are stale; the cases cover 1.0, 1.1 and 1.2 features | same |
| Normative refs (§2.1) | 3GPP 23.003, RFC 7252, ETSI 102 221, GP SCP02, IOPPROC 1.13, LwM2M AD 1.0, Core/Transport 1.1, RFC 7641, PKCS#15, RFC 2119, 2234, 4122, 5246, 5289, 5487, 6347, 6655, 6690 | adds BCP 14, RFC 8174 |
| Numbering (§3.1) | `LightweightM2M-<y.z>-int-<n>`. `y.z` is the release that introduced the case. The 1.2 ETS reuses `1.1-int-*` IDs for everything except nine new `1.2-int-*` cases | same |
| Conformance cases (§5) | "None." Only interoperability cases exist | same |
| Configurations | Appendix C, C.1-C.26 (§2 below) | same, no change |
| Coverage / EVP | App. D "Core Test Coverage" is empty ("New Appendix"). App. E.1 TestFest entry criteria: **int-101, int-102, int-201, int-203**. E.2 is an empty table | same |

### 1.1 Test tools assumed by the ETS

- Every case says `Tool: n/a`, `Test code: n/a`. The ETS defines no tool or scripted harness.
- App. B.1 example setup:
  - an M2M device with the LwM2M client;
  - a computer running the browser UI of the LwM2M Server;
  - the LwM2M Server software;
  - a provisioned USIM;
  - an external appliance (light, sensor, motor);
  - measurement software that shows LwM2M and CoAP messages ("GUI to trigger the chosen test cases" plus a message-flow view).
- Certificates: C.17, C.18, C.19 and C.21 point to "certificates & keys ... provided in a separate ZIP file" (`client.crt`, `client.key`, `server.crt`, created for `server.example.com`). **No ZIP is published next to the 1.2 or 1.2.1 PDFs.** The only key archive in `ETS/` is inside `OMA-ETS-LightweightM2M-V1_0_2-20180815-A.zip` (`example-keys.zip`, see standards.md §1.2). We must generate our own.
- DNS: int-402 needs `server.example.com` to resolve to the server. int-403 needs `server-fail.example.com` to resolve to the same server.
- int-105 needs a way to "spoof de-register" in NoSec mode, i.e. a raw CoAP tool.
- int-19 needs an MQTT broker. int-10 needs an EST server inside the BS.
- So the server needs an operator API or CLI that can issue every DM, IR and BS operation and record what comes back. That role is the "browser interface" in B.1.

### 1.2 Changes in 1.2.1-C vs 1.2-A (from a normalized text diff)

| Where | Change |
|---|---|
| §3.1, everywhere | Lower-case must/should/shall became BCP 14 capitals (MUST/SHOULD/SHALL/MAY), and "NOT RECOMMENDED" was added to the keyword list. Many lines change, but the meaning does not, except where noted below |
| int-1 (6.1.1.2) | Precondition **C.14 → C.13**. Step 2 now refers to "1-SetOfValues" instead of "0-SetOfValue_1 and 0-SetOfValue_2"; the table is renamed "1-SetOfValues" with the same content |
| int-2 (6.1.1.3) | Precondition **C.14 → C.13**. Step 2 now uses "2-SetOfValues" (same cert-mode content) |
| int-8 (6.1.1.9) | Step 2 refers to "8-SetOfValues", and an **8-SetOfValues table is added** (the PSK C.1 server account: `/0/0/0..5,10`, `/1/0/0,1,6,7`). 1.2-A referenced a set it never defined |
| int-215 | The TLV sample annotation lines are reordered. The bytes do not change |
| **int-229 (6.3.19)** | **Voided**: "See LightweightM2M-1.1-int-280 that specifies pass criteria in more detail" |
| App. A | New history row (12 Mar 2024) |

There are no new test IDs, no removed IDs (int-229 keeps its heading), no configuration changes and no TOC changes.

---

## 2. Configurations (Appendix C)

App. C preamble: a configuration is the **minimum**. A superset may be used if it still contains the minimum features. All are written as SenML-JSON-like listings. `vd` = opaque in Base64.

Shorthand used below:
- **PSK-SA** (PSK server account) = `/0/0/0`=<LwM2M Server URI>, `/0/0/1`=false, `/0/0/2`=0 (PSK), `/0/0/3`=<PSK Identity> (opaque), `/0/0/4`=n/a, `/0/0/5`=<Secret Key> (opaque), `/0/0/10`=1.
- **DEV-MIN** = `/3/0/0` Manufacturer, `/3/0/1` Model, `/3/0/2` Serial, `/3/0/3` Firmware Version, `/3/0/11/0`=0, `/3/0/16`="U".
- **SRV-MIN** = `/1/0/0`=1, `/1/0/1`=86400, `/1/0/6`=false, `/1/0/7`="U".
- **SRV-EXT** = SRV-MIN + `/1/0/2`=1 (pmin), `/1/0/3`=10 (pmax), `/1/0/5`=86400 (disable timeout).
- **EXE-STD** = Server implements `/1/x/8` Registration Update Trigger, and Device implements `/3/0/4` Reboot.

| Cfg | Name / purpose | Exact content | Used by |
|---|---|---|---|
| **C.1** | Basic Configuration 1: PSK, no SMS | PSK-SA + SRV-MIN + DEV-MIN; EXE-STD | most DM/IR cases, 101-105, 110, 201-256, 304-311, 401, 680, 685 |
| C.2 | Basic 2: PSK + SMS | C.1 plus `/0/0/6`=3 (SMS security mode), `/0/0/7`=<KIc,KID,SPI,TAR>, `/0/0/8`=<SMS secret keys>, `/0/0/9`=<MSISDN>; `/3/0/16`="US". EXE-STD (the Reboot ID is printed blank, "Resource ID: - Reboot") | none |
| **C.3** | Configuration 3 (superset of C.1) | PSK-SA + SRV-EXT (`/1/0/6` printed as `"vd":false`, **[ETS erratum]** should be `vb`) + DEV-MIN with `/3/0/11`=0 (written without instance). EXE-STD + optional `/1/0/4` Disable | 103, 205, 212, 215, 220; restore target of 651 |
| **C.4** | Configuration 4 (superset of C.3): battery resources | C.3 + `/3/0/6/0`=1 (internal battery), `/3/0/6/1`=2 (external battery), `/3/0/7/0`=0 and `/3/0/7/1`=0 (mV), `/3/0/8/0`=0 and `/3/0/8/1`=0 (mA), `/3/0/9`=0 (battery level). EXE-STD + `/1/0/4` | 260, 264, 266, 301-303, 312, 313 |
| **C.5** | Configuration 5 (superset of C.1): Connectivity Monitoring | PSK-SA + SRV-EXT + DEV-MIN + full `/4/0`: 0 Network Bearer, 1/0..1 Available Bearers, 2 RSSI, 3 Link Quality, 4/0..1 IP, 5/0..1 Router IP, 6 Link Utilization, 7/0..1 APN, 8 Cell ID, 9 SMNC, 10 SMCC. EXE-STD | 111, 701, 710 |
| **C.6** | Configuration 6 (superset of C.4): all optional Device resources | C.4 + `/3/0/10` Memory Free, `/3/0/13` Current Time (`"t"`), 14 UTC Offset, 15 Timezone, 17 Device Type, 18 HW Version, 19 SW Version, 20 Battery Status, 21 Memory Total, 22 ExtDevInfo (`vlo`). EXE-STD + `/1/0/4` + `/3/0/5` Factory Reset + `/3/0/12` Reset Error Code | 257, 651, 652 |
| C.7 | Configuration 7 (superset of C.1): Location | PSK-SA + SRV-EXT + DEV-MIN + `/6/0/0`=43.5723, `/6/0/1`=153.21760, `/6/0/2`=140, `/6/0/3`=100, `/6/0/4`=opaque "0" (3GPP TS 23.032), `/6/0/5`=1367491215 (time), `/6/0/6`=3. EXE-STD | 801, 810 |
| **C.8** | Configuration 8: Firmware Update | PSK-SA + SRV-EXT + DEV-MIN + `/5/0/0` Package (opaque), `/5/0/1` Package URI, 3 State, 5 Update Result, 6 Package Name, 7 Package Version, 8 FW Update Protocol Support ("CoAP only if absent"), 9 Delivery Method. EXE-STD + `/1/0/4` + `/5/0/2` Update | 751-779 |
| C.9 | Configuration 9 (superset of C.1): Connectivity Statistics | PSK-SA + SRV-EXT + DEV-MIN + `/7/0/0..5`=0, `/7/0/8`=0 (Collection Period). EXE-STD + `/7/0/6` Start, `/7/0/7` Stop (printed as "Device Object ID:7") | 901, 905, 910 |
| C.10 | Basic Connectivity Mgmt 10 | PSK-SA + SRV-MIN + DEV-MIN + `/10/0/4`=3600 (PSM timer), `/10/0/5`=600 (Active timer), `/10/0/6`=40, `/10/0/8`=20.48, `/10/0/9`=51.2; `/11/0/0`=<Profile Name>, `/11/0/3`=true (printed `"b"`); `/12/0/0` WLAN id, `/12/0/1`=0, `/12/0/3`=0, `/12/0/4`=<MAC> (duplicated as `"v":0`), `/12/0/8`=0, `/12/0/14`=0, `/12/0/15`=0 | 1200, 1204, 1250, 1350 |
| C.11 | Conn. Mgmt 11, Object 10 v1.1 | C.10 + `/10/0/11`=vlo "11:0" (APN profile list), `/10/0/13`=3 (Power Saving Modes), `/10/0/14`=1 (Active PSM) | 1201-1203 |
| **C.12** | Configuration 12 (superset of C.1): Portfolio | PSK-SA + SRV-EXT (`"vs "` key typo) + DEV-MIN + `/16/0/0/0`="Host Device ID #1", `/16/0/0/1`=" Host Develce Manufacturer #1", `/16/0/0/2`=" Host Device Model #1", `/16/0/0/3`=" Host Device Software Version #1" (typo and leading spaces as printed). EXE-STD | 1630, 1635 |
| **C.13** | Bootstrap Server Contact 13: PSK, no SMS | `/0/1/0`=<BS URI>, `/0/1/1`=**true**, `/0/1/2`=0, `/0/1/3`=<PSK Identity>, `/0/1/4`=n/a, `/0/1/5`=<Secret Key>; DEV-MIN. "The Mandatory Object Server (ID:1) is supported by the Client but has no Instance". The BS account lives in **Security instance 1**, which leaves instance 0 for the server account | 0, 4, 6-9, 12 (1.2.1: also 1, 2) |
| **C.14** | Multi-Servers Initial 14 | BS account at `/0/1` (as C.13, identity/key #0). Server account #1: `/0/0/*` PSK, `/0/0/10`=1, `/1/0`=SRV-EXT with SSID 1. Server account #2: `/0/2/*` PSK, `/0/2/10`=2, `/1/1`: `/1/1/0`=2, 1=86400, 2=1, 3=10, 5=86400, 6=false, 7="U". Access Control (as printed, all written to `/2/0`, **[ETS erratum]**, should be three instances): (a) obj 1 inst 0, ACL `/2/0/2/1`=15, owner 1; (b) obj 1 inst 1, ACL `/2/0/2/2`=15, owner 2; (c) obj 3 inst 0, ACL `/2/0/2/2`=15, owner MAX_ID ("Server #2 & #1 have full access"). DEV-MIN | 950, 951 (1.2-A: 1, 2) |
| C.15 | Binary AppData Container 15 | PSK-SA + SRV-EXT + DEV-MIN + `/19/0/0`=opaque Base64 JSON meter blob, `/19/1/0`=n/a. EXE-STD. Inst 0 = client to server data, inst 1 = server to client | 1900, 1901 |
| C.16 | Event Log 16 | PSK-SA + SRV-EXT + DEV-MIN + `/20/0/4010`=0 (LogClass), `/20/0/4011`="0,100" (LogStart), `/20/0/4013`=3 (LogStatus), `/20/0/4014`=opaque (LogData). EXE-STD | 2000, 2001 |
| C.17 | Basic 17: certificate | Like C.1 but `/0/0/0`=`coaps://server.example.com`, `/0/0/2`=2, `/0/0/3`=client.crt, `/0/0/4`=server.crt, `/0/0/5`=client.key; SRV-MIN + DEV-MIN; EXE-STD | 402 |
| C.18 | Basic 18: certificate, wrong host | C.17 with `/0/0/0`=`coaps://server-fail.example.com` (the cert is for `server.example.com`) | 403 |
| C.19 | Basic 19: CoAP over TLS, certificate | C.17 with `/0/0/0`=`coaps+tcp://server.example.com`, `/1/0/7`="T", `/3/0/16`="T" | 404, (405 as printed) |
| C.20 | Basic 20: CoAP over TLS, PSK | C.1 with `/1/0/7`="T", `/3/0/16`="T" | 406 |
| C.21 | Basic 21: EST bootstrap | `/0/0/0`=<BS URI>, `/0/0/1`=true, `/0/0/2`=**4** (EST), 3/4/5 = client.crt, server.crt, client.key ("not configured into resources 3 and 5 but instead available to EST directly"), `/0/0/10`=0; DEV-MIN | 10 |
| C.22 | Basic 22: OSCORE | `/0/0/0`=<Server URI>, 1=false, 2=**3** (NoSec), 3/4/5 n/a, 10=1, `/0/0/17`="/21/0" (OSCORE objlnk; printed as `"v":"/21/0"`); SRV-MIN with `/1/0/7`="U" or "T"; DEV-MIN; `/21/0/0`=<Master Salt Key> (as printed; Master Secret is not listed), `/21/0/1`=<Sender ID>, `/21/0/2`=<Receiver ID> | not referenced by any case (405 cites C.19; **[ETS erratum]**, the intended config is C.22) |
| C.23 | BS Contact 23: OSCORE | `/0/0/0`=<BS URI>, 1=true, 2=3, 3/4/5 n/a, 10=n/a, 17="/21/0"; DEV-MIN; `/21/0/0..2` Master Salt, Sender ID, Receiver ID | 11 |
| C.24 | Basic 24: OSCORE | `/0/1/0`=<Server URI>, 1=false, 2=3, 10=1, 17="/21/1"; `/1/1/0`=1, 1=86400, 6=false, 7="U"; DEV-MIN; `/21/1/0..2` | 11 (target written by BS) |
| C.25 | BS Contact 25: MQTT | `/0/1/0`=<MQTT broker URI for BS>, 1=true, 2=0 (PSK), 3=<PSK Identity>, 5=<Secret Key>, `/0/1/26`="/24/0" (MQTT server objlnk), `/0/1/27`="/23/0" (COSE objlnk); DEV-MIN with `/3/0/16`="M"; `/23/0/0`=<Key Identifier>, `/23/0/1`=0, `/23/0/2`=<Secret Key>; `/24/0/3`=true, `/24/0/6`=<Client Identifier>. Server Object has no instance | 19 |
| C.26 | Basic 26: MQTT | `/0/1/0`=<MQTT broker URI>, 1=false, 2=0, 3/5 PSK, 10=1, 26="/24/1", 27="/23/1"; `/1/1/0`=1, 1=86400, 6=false, 7="**M**"; DEV-MIN with 16="M"; `/23/1/0..2` COSE key; `/24/1/6`=<Client Identifier> | 19 |

---

## 3. Test-case table (every case in ETS INT 1.2, §6)

Legend:
- **Ver**: the version in the ID. "1.1" cases also apply to 1.2 clients; "(≥1.0)" means the operation exists in 1.0.
- **Role**: what is actually exercised. **C** = client, **S** = LwM2M Server, **BS** = Bootstrap-Server. The ETS "Test Object" field reads "Client and Server" for all cases except int-6 ("Client") and int-10/404 ("Client, Bootstrap-Server and Server").
- **Zephyr**: `zephyr-interop.md` test function. `<mod>::int_N` = `test_<mod>.py::test_LightweightM2M_1_1_int_N`. "no" = not automated.
- **Placeholder**: the ETS heading exists, but the body is "<Test Case(s) to fill-up>", "paragraph" or empty. **Delegated**: the ETS says another case's status counts for this one.
- Steps use `S→C` (server or BS sends to client) and `C→S`. CF = Content-Format, Acc = Accept.

### 3.1 Bootstrap interface (§6.1, IDs 0-99)

| ID (§) | Title | Ver | Role | Pre / Cfg | Steps | Pass criteria | Req IDs | Zephyr |
|---|---|---|---|---|---|---|---|---|
| 1.1-int-0 (6.1.1.1) | Client Initiated Bootstrap | 1.1 (≥1.0; Discover 1.1) | BS, C | C.13. Client supports CI bootstrap. No `/1` instance | 1. C→BS `POST /bs?ep={ep}` using Security inst 1 (BS account). 2. BS→C two Bootstrap-Writes `PUT /0` and `PUT /1` with **0-SetOfValues** = PSK-SA + SRV-MIN (C13+C1; `/1/0/7` printed `"bs":"U"`, **[ETS erratum]** should be `vs`). 3. BS→C Bootstrap-Discover `GET /` Acc 40. 4. BS→C Bootstrap-Finish `POST /bs` | 1. BS answered 2.04 to Request. 2-3. Writes received and answered 2.04. 4. Discover 2.05 containing `lwm2m="1.1",</0/0>;ssid=1,</0/1>,</1/0>;ssid=1,</3/0>`. 5. Finish 2.04 (no inconsistency) | BS-01, BS-02, BS-03, BS-05, BS-07, BS-08, BS-09 | bootstrap::int_1 (via `verify_…_int_0`) |
| 1.1-int-1 (6.1.1.2) | Client Initiated Bootstrap Full (PSK) | 1.1 | BS, C, S | **1.2-A: C.14; 1.2.1-C: C.13**. CI bootstrap. No `/1` instance. PSK DTLS capable | 1-4 as int-0, uploading C.1 (0-/1-SetOfValues: PSK-SA, SRV-MIN with `/0/0/3`,`/0/0/5` as `vs`). Device `/3` is filled by the client. 5. C registers to the provisioned server with PSK as in int-401 | 1-5 as int-0 (Discover same payload). 6. int-401 passes | BS-01..03, BS-05, BS-07, BS-10, SEC-03..06, REG-01, REG-05 | bootstrap::int_1 (+ `verify_…_int_101`, `verify_…_int_401`) |
| 1.1-int-2 (6.1.1.3) | Client Initiated Bootstrap Full (Cert) | 1.1 | BS, C, S | **1.2-A: C.14; 1.2.1-C: C.13**. Cert-capable client | 1-4 as int-1 with the cert set: `/0/0/0`=`coaps://server.example.com`, `/0/0/2`=2, 3=client.crt, 4=server.crt, 5=client.key, 10=1; SRV-MIN. 5. Register with cert as int-402 | as int-1. 6. int-402 passes | BS-01..03, BS-05, BS-07, BS-10, SEC-03, SEC-10, SEC-06 | no (README: not implemented) |
| 1.1-int-3 (6.1.1.4) | Simple Bootstrap from Smartcard | 1.1 | C, S | Client supports Smartcard bootstrap. C.1 with Server URI "X", lifetime 86400, registered to X. Smartcard holds a Bootstrap Information file (Annex G/H of TS 1.0.2) with server "Y", lifetime 43200 | 1. C registered to X. 2. X→C `GET /1/0/1`. 3. Power off, insert card, power on. 4. C registers to Y. 5. Y→C `GET /1/0/1` | 1. int-101 A and B pass for X. 2. Read 2.05 = 86400. 3. int-101 passes for Y. 4. Read 2.05 = 43200 | REG-01, REG-05, REG-03, DM-02 | no (no support) |
| 1.1-int-4 (6.1.1.5) | Bootstrap Delete | 1.1 | BS, C | C.13. CI bootstrap | 1. C→BS `POST /bs?ep=`. 2. Standard CI flow (Discover, Writes, Finish **[ETS erratum: text says "Bootstrap" for Finish]**) as int-0. 3. BS→C Discover `GET` Acc 40 + Bootstrap-Read `GET /1`. 4. BS→C `DELETE /0` and `DELETE /1`. 5. Discover + Read `/1` again. 6. Clean-up: standard CI flow ending with Finish | 1. Request 2.04. 2. Client answers 2.04 to each. 3. Discover and Read 2.05 show the new `/1` instance. 4. Both deletes 2.02. **The BS account (its Security instance) is not affected.** 5. Replies show the `/1` instance is gone. 6. Writes 2.04, Finish 2.04, then C registers | BS-04, BS-05, BS-06, BS-03, BS-07 | bootstrap::int_4 (variant: S executes `/1/0/9`, BS config `toDelete:["/0","/1"]`) |
| 1.1-int-5 (6.1.1.6) | Server Initiated Bootstrap | 1.1 | S, C, BS | C registered | 1. S→C `POST /1/x/9` (Bootstrap-Request Trigger) | 1. C answers 2.04. 2. C sends `POST /bs?ep=` as in int-0. 3. Bootstrap ends with Finish as in int-0 | BS-11, REG-19, BS-01, BS-07, DM-08 | bootstrap::int_5 |
| 1.1-int-6 (6.1.1.7) | Bootstrap Sequence | 1.1 | C (Test Object = "Client") | C.13. ≥2 bootstrap modes | 1. Enable the device | Order: (1) Smartcard if present; (2) otherwise Factory Bootstrap; (3) register to any configured `/1` servers; (4) if all fail or none exist, Client Initiated Bootstrap; (5) Server Initiated Bootstrap is possible only if the BS account is retained | BS-01 (client side), REG-20 | bootstrap::int_6 |
| 1.1-int-7 (6.1.1.8) | Fallback to bootstrap | 1.1 | C, BS, S | C.13. Valid BS provisioned | 1. Provision a non-existent server (factory bootstrap). 2. Start | 1. Registration fails. 2. C performs CI bootstrap as int-0. 3. BS accepts. C then registers as in int-401 | BS-01, REG-20, BS-07 | bootstrap::int_7 (slow; 600 s wait) |
| 1.1-int-8 (6.1.1.9) | Bootstrap Read | 1.1 | BS, C | C.13. CI-capable | 1. C→BS `POST /bs?ep=`. 2. BS→C `PUT /0`, `PUT /1` (1.2-A: undefined "0-SetOfValue_1/_2"; **1.2.1-C: 8-SetOfValues** = PSK-SA + SRV-MIN). 3. BS→C Bootstrap-Write Access Control: `/2/0/0`=3, `/2/0/1`=0, `/2/0/2/0`=31, `/2/0/3`=65535. 4. BS→C Bootstrap-Read `GET /1` and `GET /2`. 5. Finish `POST /bs` | 1. Request 2.04. 2-3. Writes answered "2.04 Created" (sic). 4. Both reads 2.05. 5. Finish 2.04 | BS-06, BS-03, BS-07, DM-14 | no (README: "cannot be implemented from client side") |
| 1.1-int-9 (6.1.1.10) | Bootstrap and Configuration Consistency | 1.1 | BS, C | C.13. CI-capable | 1. C→BS `POST /bs?ep=`. 2. BS→C only `PUT /1/x/5` = 86400 (Disable Timeout, not mandatory). 3. BS→C Finish | 1. Request 2.04. 2. Write 2.04. 3. **Finish answered 4.06 Not Acceptable**: the client rejects the config because the mandatory Server account (Security + Server mandatory resources) is missing | BS-07 (4.06 path), BS-03 | no (not implemented) |
| 1.1-int-10 (6.1.1.11) | Client Initiated Bootstrap Full (EST) | 1.1 | C, BS (EST), S | C.21. Client supports cert + EST and CI bootstrap. No `/1` instance | 1. C connects to BS using EST: local key pair, CSR to BS. 2. BS returns a cert. 3. C→BS `POST /bs?ep=`. 4. BS→C Bootstrap-Write with 0-SetOfValues: `/0/0/0`=`coaps://server.example.com`, `/0/1/1`=false, `/0/1/2`=2, `/0/1/3`="" and `/0/1/5`="" (use EST), `/0/1/4`=server cert, `/0/1/10`=1 (mixed `/0/0` and `/0/1` paths, **[ETS erratum]**); SRV-MIN. 5. C answers 2.04. 6. Finish. 7. C registers to S | 1. Request 2.04. 2-3. Write received, 2.04. 4. EST exchange succeeds and C gets a cert. 5. C registers | BS-10, SEC-08 (mode 4), BS-01, BS-03, BS-07 | no |
| 1.2-int-11 (6.1.2) | Client Initiated Bootstrap (OSCORE) | 1.2 | BS, C | C.23. CI bootstrap. No `/1` instance | 1. C→BS `POST /bs?ep=` protected by OSCORE context `/21/0` (C.23). 2. BS→C three writes `PUT /0`, `PUT /21`, `PUT /1` (C23+C24): `/0/1/0`=<Server URI>, 1=false, 2=3, 10=1, 17="/21/1"; `/21/1/0..2` Master Salt, Sender ID, Receiver ID; `/1/1/0`=1, 1=86400, 6=false, 7="U". 3. Discover `GET /` Acc 40. 4. Finish | 1. 2.04. 2-3. Writes 2.04. 4. Discover 2.05 = `lwm2m="1.1",</0/0>,</0/1>;ssid=1;uri="coap://server_1.example.com",</1/0>;ssid=1,</3/0>,</21/0>,</21/1>;ssid=1` (says lwm2m 1.1 and `/1/0` although `/1/1` was written, **[ETS erratum]**). 5. Finish 2.04 | BS-13, SEC-15, BS-01, BS-03, BS-05, BS-07 | no |
| 1.2-int-12 (6.1.2.1) | Bootstrap via Bootstrap-Pack-Request | 1.2 | BS, C, S | C.13. Client and BS support Bootstrap-Pack. No `/1`. PSK | 1. C→BS **`GET /bspack?ep={ep}&acc={BS account instances}`** using Security inst 1. 2. BS answers **2.05** with instances of `/0` and `/1` = 0-SetOfValues (PSK-SA + SRV-MIN; `/3` is filled by the client to complete C.1). 3. C registers with PSK as int-401 | 1. "BS received 2.04 ... BOOTSTRAP-REQUEST" (**[ETS erratum]**: a Pack-Request gets 2.05). 2. C received the Pack response for C.1. 3. int-401 passes | BS-12, BS-02, BS-10, REG-01 | no (Zephyr has no 1.2) |
| 1.2-int-19 (6.1.2.2) | Client Initiated Bootstrap over MQTT | 1.2 | BS (MQTT), C | C.25 present, C.26 minimum. Client supports MQTT, COSE, PSK. BS supports MQTT and has subscribed to `+/lwm2m/bs/+` | 1. C uses `/0/0` (as printed; C.25 puts it in `/0/1`) + `/23/0` to open a secure MQTT connection. 2. C subscribes `+/lwm2m/bs/<ep>`. 3. C publishes to `lwm2m/bs/<ep>` the payload `{operation=>0, token=>uint, pct=>60}`. 4. BS publishes 4 Bootstrap-Writes (`/0`, `/1`, `/23`, `/24`) with C.26 values. 5. BS Discover `{operation=>4, token, uri=>"/"}`. 6. Finish | 1. CONNACK. 2. SUBACK. 3. PUBACK. 4-5. Writes received, 2.04. 6. Discover 2.05 = `lwm2m="1.2",</0/0>,</0/1>;ssid=1;uri="mqtt://server_1.example.com",</1/0>;ssid=1,</3/0>,</23/0>,</23/1>;ssid=1,</24/0>,</24/1>;ssid=1`. 7. Finish 2.04 | REG-18 (binding M), BS-01, BS-03, BS-05, BS-07 | no |

### 3.2 Registration interface (§6.2, IDs 100-199)

| ID (§) | Title | Ver | Role | Pre / Cfg | Steps | Pass criteria | Req IDs | Zephyr |
|---|---|---|---|---|---|---|---|---|
| 1.1-int-101 (6.2.1) | Initial Registration | 1.1 (≥1.0) | C, S | C.1. Device on. Bootstrap done | 1. C→S `POST /rd?ep&lt&lwm2m&b&sms` + link-format object list | 1. S receives ep (optional), lifetime, **`lwm2m=1.1`**, binding (opt), SMS (opt), objects/instances (optionally `ver`), **without `/0` and `/21`**. 2. C gets **2.01 Created** | REG-01..03, REG-05, REG-07..11, REG-18, VER-01 | nosec::int_101; bootstrap `verify_…_int_101` |
| 1.1-int-102 (6.2.2) | Registration Update | 1.1 (≥1.0) | S, C | C.1. Registered. S prepared to set lifetime 20 s | 1. S→C `PUT /1/0/1` = 20. 2. C→S Update `POST /rd/{loc}?lt=20`. 3. (optional) before expiry C sends an Update with no params | 1. Write 2.04. 2. S got the Update with lt=20. 3. C got 2.04. 4. Either a parameterless Update arrives, or **the server de-registers the client after 20 s** | REG-03, REG-13, REG-14, DM-05 | lwm2m::int_102 (writes lifetime+10, not 20) |
| 1.1-int-103 (6.2.3) | Deregistration | 1.1 (≥1.0) | S, C | C.3. Registered | 1. S→C `POST /1/0/4` (Disable). 2. C→S `DELETE /rd/{loc}` | 1. Exec 2.04. 2. C gets **2.02 Deleted**. 3. Client removed from the server's registration DB | REG-16, DM-08 | lwm2m::int_103 |
| 1.1-int-104 (6.2.4) | Registration Update Trigger | 1.1 (≥1.0) | S, C | C.1. Registered with lt=20 (int-102) | 1. Before expiry S→C `POST /1/0/8`. 2. C→S Update `POST /rd/{loc}` | 1-2. C received it; exec 2.04. 3. Update has no parameters. 4. C gets 2.04. 5. **C is still registered after the initial lifetime has passed** (S refreshed the lifetime) | REG-19, REG-14, REG-13 | lwm2m::int_104 |
| 1.1-int-105 (6.2.5) | Discarded Register Update | 1.1 | **S**, C | C.1. Registered | 1. S→C Write lifetime low (e.g. 60) "by CoAP POST on /1/x/1" (**[ETS erratum]**: resource-level Write is PUT). 2. Make S consider the device de-registered (expected lifetime 1 s, delete device/location, or in NoSec spoof a De-register). 3. Wait for C's `POST /rd/{loc}` | 1. C sends an Update within the lifetime. 2. S considers the device de-registered; C is unaware. 3. **S answers the Update 4.04 Not Found.** 4. C re-registers with `POST /rd` (int-101) | REG-13, REG-14, REG-16, REG-06 | nosec::int_105 (spoofed DELETE from another source; needs CoAPthon3) |
| 1.1-int-106 (6.2.6) | TCP Binding | 1.1 | C, S | Device on. Bootstrap info with a server over TCP | 1. C registers | a) TCP session. b) **CSM exchanged** both ways. c) `POST /rd` with ep, lt, lwm2m, **b=T**, sms (opt), link-format objects (e.g. `</1/2>,</2>,</3/0>,…,</10>;ver="1.1"`), no `/0` or `/21`. d) 2.01 Created | REG-01, REG-18, REG-07, REG-09 (RFC 8323) | no |
| 1.1-int-107 (6.2.7) | Extending the lifetime | 1.1 (≥1.0) | S, C | Device on. Server configured for U | 1. C registers with b=U, lt=60. 2. S→C `PUT /1/x/1` = 120. 3. Wait for the next Update | 1. 2.01. 2a. Write 2.04. 2b. C immediately sends `POST /rd/{loc}?lt=120`. 2c. **S parses and accepts it, 2.04**. 3. The next Update arrives within the lifetime | REG-03, REG-14, DM-05 | lwm2m::int_107 (slow) |
| 1.1-int-108 (6.2.8) | Turn on Queue Mode | 1.1 | S, C | Device on. Server configured for U | 1. C registers `POST /rd?b=U&Q…` | 2.01. **S considers the client to be in Queue Mode** | QM-01, REG-18 | lwm2m::int_108 (checks Leshan `queuemode`) |
| 1.1-int-109 (6.2.9) | Behavior in Queue Mode | 1.1 | **S**, C | Device on. U. MAX_TRANSMIT_WAIT = 93 s on both sides | 1. Register `b=U&Q`. 2. S→C Write lifetime 240 "CoAP POST on /1/x/1" (**[ETS erratum]**, PUT). 3. Wait 120 s. 4. Operator orders a Read (e.g. `/1/x/1`). 5. Wait for the next Update | 1. 2.01. 2a. 2.04. 2b. `POST /rd/{loc}?lt=240`. 2c. 2.04. 3. After MAX_TRANSMIT_WAIT, C is considered unreachable. 4. **Nothing is sent; S queues the Read.** 5. S accepts the Update and **only then sends the queued `GET /1/x/1`** | QM-01..04, REG-14 | lwm2m::int_109 (slow; client-side log checks only) |
| 1.2-int-110 (6.2.10) | Initial Registration using Profile ID | 1.2 | **S**, C | C.1. Client: 32-bit Profile ID, SHA-256. **Server: has C.1 on its list of known profiles**, i.e. matches the 32-bit truncated SHA-256 | 1. C→S `POST /rd` | 1. S receives ep (opt), lt, **lwm2m=1.2**, b (opt), **`pid=6:27354b9a`** (for object list `/1/0`, `/3/0`), **no object list**. 2. 2.01 | REG-21, REG-01, REG-07 | no |
| 1.2-int-111 (6.2.11) | Registration with Profile ID + Object List | 1.2 | **S**, C | C.5. Same profile support (C.1 known) | 1. C→S `POST /rd` | 1. lwm2m=1.2, `pid=6:27354b9a`, plus the objects not covered by the profile: printed as `<lwm2m=1.2;lt=86400;pid=6:27354b9a,/4/0>` (i.e. query params + `</4/0>`). 2. 2.01. S must merge the profile list with the explicit list | REG-21, REG-15 | no |

### 3.3 Device Management & Service Enablement (§6.3, IDs 200-299)

| ID (§) | Title | Ver | Role | Pre / Cfg | Steps | Pass criteria | Req IDs | Zephyr |
|---|---|---|---|---|---|---|---|---|
| 1.1-int-201 (6.3.1) | Query basic info, Plain Text | 1.1 (≥1.0) | S, C | C.1. Registered. Plain Text supported | 1. S→C `GET /3/0/0`, `/3/0/1`, `/3/0/2`, each with Acc 0 | 2.05 for each, with plain-text values of Manufacturer, Model, Serial | DM-02, FMT-01, FMT-03, DT-02 | lwm2m::int_201 |
| 1.1-int-202 (6.3.2) | Query basic info, Opaque | 1.1 | n/a | **Placeholder** (body is just "paragraph") | none | none | (FMT-01 opaque) | no |
| 1.1-int-203 (6.3.3) | Query basic info, TLV | 1.1 (≥1.0) | S, C | C.1. Registered | 1. S→C `GET /3/0` Acc 11542. 2. Payload bits 7-6 = 00 (Object Instance TLV wrapping resource TLVs). 3. bits 7-6 = 11 (Resource with value) | 2.05 TLV with Manufacturer (0), Model (1), Serial (2), FW Version (3), Error Code (11), Supported Binding (16) = "U" | DM-02, FMT-01, FMT-04 | lwm2m::int_203 |
| 1.1-int-204 (6.3.4) | Query basic info, JSON | 1.1 (≥1.0) | S, C | C.1. JSON (11543) supported | 1. S→C `GET /3/0` Acc 11543 | 2.05 JSON with 0, 1, 2, 3, 11, 16="U" | DM-02, FMT-01, FMT-02 | lwm2m::int_204 |
| 1.1-int-205 (6.3.5) | Set basic info, Plain Text | 1.1 (≥1.0) | S, C | C.3. Plain Text. Registered | 1. S→C `PUT /1/0/2`=101, `PUT /1/0/3`=1010, `PUT /1/0/5`=2000 (CF 0). 2. S→C `GET /1/0` Acc TLV. 3. Repeat 1-2 with the C.3 values (1, 10, 86400) | 1. 2.04 each. 2. 2.05 TLV with the new values. 3. C.3 restored | DM-05, DM-06, FMT-01 | lwm2m::int_205 (reads each resource back, not `/1/0` TLV) |
| 1.1-int-210 (6.3.6) | Set basic info, Opaque | 1.1 | n/a | **Placeholder** (heading only) | none | none | (FMT-01) | no |
| 1.1-int-211 (6.3.7) | Query basic info, CBOR | 1.1 | S, C | C.1. Registered as 1.1. CBOR (60) | 1. S→C `GET /1/0/0`, `/1/0/6`, `/1/0/7`, each Acc 60 | 2.05 CBOR: SSID 1, Notification Storing false, Binding "U" | DM-02, FMT-01 (CBOR) | lwm2m::int_211 |
| 1.1-int-212 (6.3.8) | Set basic info, CBOR | 1.1 | S, C | C.3. CBOR. Registered as 1.1 | 1. S→C `PUT /1/0/2`=101, `/1/0/3`=1010, `/1/0/6`=true (CF 60). 2. S→C `GET /1/0` (client-preferred format) | 1. 2.04 each. 2. 2.05 with those values | DM-05, FMT-01, FMT-03 | lwm2m::int_212 |
| 1.1-int-215 (6.3.9) | Set basic info, TLV | 1.1 (≥1.0) | S, C | C.3. Registered. S saved the initial `/1/0` values | 1. S→C `POST /1/0` (partial update) TLV 215-SetOfValues: `C1 02 65` (/1/0/2=101), `C2 03 03 F2` (/1/0/3=1010), `C2 05 07 D0` (/1/0/5=2000), `C1 06 01` (/1/0/6=true), `C1 07 55` (/1/0/7="U"). 2. `GET /1/0`. 3. S→C `PUT /1/0` (replace) with the saved values. 4. `GET /1/0` | 1. 2.04, 2.05, 2.04, 2.05. 2. Values match 215 (pass text says "step 4"). 3. C.3 values restored ("step 6", **[ETS erratum]**) | DM-05, DM-06, FMT-04 | lwm2m::int_215 |
| 1.1-int-220 (6.3.10) | Set basic info, JSON | 1.1 (≥1.0) | S, C | C.3. Saved initial values | 1. S→C `POST /1/0` CF 11543 220-SetOfValues `[{"bn":"/","n":"1/0/2","v":0101},{"n":"1/0/3","v":1010},{"n":"1/0/5","v":2000},{"n":"1/0/6","bv":true},{"n":"1/0/7","sv":"U"}]` (leading-zero number is invalid JSON, **[ETS erratum]**). 2. Read the targeted resources. 3. `PUT /1/0` with the saved values. 4. `GET /1/0` | 2.04/2.05/2.04/2.05. Step 2 values = 220 set; step 4 = C.3 | DM-05, FMT-01, FMT-02 | lwm2m::int_220 |
| 1.1-int-221 (6.3.11) | Operations on Security Object | 1.1 (≥1.0) | C, S | C.1 | 1. S→C `GET /0`. 2. `PUT /0/0/0`. 3. Write-Attributes `PUT /0?pmin=30&pmax=45` | All three answered **4.01 Unauthorized** | DM-11, DM-07 | lwm2m::int_221 (pmin=10) |
| 1.1-int-222 (6.3.12) | Read on Object | 1.1 (≥1.0) | S, C | C.1 | 1. `GET /1`. 2. `GET /3` | 1. 2.05 with the instance and all its resources. 2. 2.05 with the single `/3/0` instance and all its resources | DM-02, FMT-04/05 | lwm2m::int_222 |
| 1.1-int-223 (6.3.13) | Read on Object Instance | 1.1 (≥1.0) | S, C | C.1 | 1. `GET /1/x`. 2. `GET /3/0` | 2.05 with all resources for each | DM-02 | lwm2m::int_223 |
| 1.1-int-224 (6.3.14) | Read on Resource | 1.1 (≥1.0) | S, C | C.1. Mandatory `/1` resources | 1. `GET /1/x/0`, `/1/x/1`, `/1/x/6`, `/1/x/7`. 2. `GET /3/0/16` | 2.05 with each value | DM-02 | lwm2m::int_224 (no `/3/0/16`) |
| 1.1-int-225 (6.3.15) | Read on Resource Instance | 1.1 | S, C | C.1 | 1. `GET /3/0/11/x` | 2.05 containing only the `/3/0/11/x` value | DM-02 | lwm2m::int_225 |
| 1.1-int-226 (6.3.16) | Write (Partial Update) on Object Instance | 1.1 (≥1.0) | S, C | C.1 | 1. S→C `POST /1/x`: lifetime 61, notification storing true. 2. `GET /1/x`. 3. `POST /1/x` restoring the previous values | 1. **One** message to the instance; 2.04; **C sends an Update with the new lt**. 2. 2.05 shows the values. 3. 2.04 | DM-05, REG-03, REG-14 | lwm2m::int_226 (lifetime 60) |
| 1.1-int-227 (6.3.17) | Write (replace) on Resource | 1.1 (≥1.0) | S, C | C.1 | 1. `PUT /1/x/1`=63. 2. `GET /1/x/1`. 3. `PUT` restore ("/1/x" printed, **[ETS erratum]**) | 1. 2.04 + Update with the new lt. 2. 2.05 = 63. 3. 2.04 + Update | DM-05, REG-03, REG-14 | lwm2m::int_227 |
| 1.1-int-228 (6.3.18) | Write on Resource Instance | 1.1 | S, C | C.1 + Portfolio `/16` with an instance whose Identity (0) has ≥2 instances | 1. S→C Write "CoAP POST" `/16/x/0/y` = "test" (resource-instance Write is PUT per T §6.4.4; Leshan and Zephyr use PUT). 2. `GET /16/x/0/y` | 1. Payload carries only instance y; 2.04. 2. 2.05 with only y = "test" | DM-05, FMT-05 | lwm2m::int_228 (creates `/16/0` first) |
| 1.1-int-229 (6.3.19) | Read-Composite Operation | 1.1 | S, C | C.1. **1.2.1-C: Voided, see int-280** | 1. S→C FETCH with paths `/3`, `/1/x`, `/1/x/1`, `/3/0/11/0`; Acc SenML CBOR or JSON | 2.05 with the requested data in SenML CBOR or JSON | DM-12, FMT-05 | lwm2m::int_229 |
| 1.1-int-230 (6.3.20) | Write-Composite | 1.1 | S, C | C.1 + Portfolio. "Not mandatory" | 1. S→C **one iPATCH**: `/1/x/1`=61, `/1/x/6`=true, `/16/0/0/0..3` = "aa", "bb", "cc", "dd" (SenML CBOR or JSON). 2. `GET /1/x` and `GET /16` | 1. Single iPATCH. 2.04. C sends an Update with the new lt. 2. 2.05 matching the writes | DM-12, FMT-05 | lwm2m::int_230 |
| 1.1-int-231 (6.3.21) | Query, SenML JSON | 1.1 | S, C | C.1 | `GET /1`, `GET /3/0`, `GET /3/0/16`, `GET /3/0/11/x`, each Acc 110 | 2.05 with CF 110 at O, OI, R and RI levels | DM-02, FMT-05 | lwm2m::int_231 |
| 1.1-int-232 (6.3.22) | Query, SenML CBOR | 1.1 | S, C | C.1 | Same four reads with Acc 112 | 2.05 with CF 112 | DM-02, FMT-05 | lwm2m::int_232 |
| 1.1-int-233 (6.3.23) | Set, SenML CBOR | 1.1 | S, C | C.1 + Portfolio | 1. `POST /1/x` CF 112: lifetime 61, storing true. 2. `GET /1`. 3. `POST` or `PUT /16/0/0/0` = "test_value" CF 112. 4. `GET /16`. 5. Write `/1/x/1` = 63 CF 112 (path printed `/1/x`). 6. `GET /1/x/1` | 1. 2.04 + Update. 2. 2.05 shows step 1. 3. 2.04. 4. 2.05 shows step 3. 5. 2.04 + Update. 6. 2.05 = 63 | DM-05, FMT-05, REG-14 | lwm2m::int_233 |
| 1.1-int-234 (6.3.24) | Set, SenML JSON | 1.1 | S, C | as int-233 | Same as int-233 with CF 110 | same | DM-05, FMT-05, FMT-02 | lwm2m::int_234 |
| 1.1-int-235 (6.3.25) | Read-Composite on root | 1.1 | S, C | C.1 | 1. FETCH body path `/`, CF and Acc SenML CBOR or JSON | 2.05 with all readable resources in the requested format. **`/0`, `/21` and `/23` absent** | DM-12, DM-11 | lwm2m::int_235 |
| 1.1-int-236 (6.3.26) | Read-Composite, partial presence | 1.1 | S, C | C.1 | 1. FETCH `/1/x`, `/1/x/1`, `/3/0/11/0` plus non-existent `/3339/0/5522`, `/3353/0/6030` | 2.05. Non-atomic best effort: **missing paths are omitted** | DM-12, DM-03 | lwm2m::int_236 |
| 1.1-int-237 (6.3.27) | Read on Object without Accept | 1.1 | S, C | C.1 | 1. `GET /1` with no Accept. 2. `GET /3` with no Accept | A/B: 2.05; CF is the client's choice (SenML CBOR or JSON). **S must decode whatever arrives** | DM-02, FMT-03, FMT-01 | lwm2m::int_237 |
| 1.1-int-241 (6.3.28) | Reboot | 1.1 (≥1.0) | S, C | C.1. Registered | 1. S→C `POST /3/0/4`. 2. C reboots and registers again (int-101) | 1. 2.04. 2. Re-registration succeeds; S replaces the old registration | DM-08, REG-12, OBS-03 | lwm2m::int_241 |
| 1.1-int-256 (6.3.29) | Write Operation Failure | 1.1 (≥1.0) | C, S | C.1. Registered as 1.1 | 1. Write SSID `/1/0/0` = 123. 2. `GET /1/0/0` | 1. **4.05 Method Not Allowed**. 2. 2.05 = 1 | DM-06, GEN-06 | lwm2m::int_256 |
| 1.1-int-257 (6.3.30) | Write-Composite (Server + Device) | 1.1 | S, C | **C.6**. 1.1. "Client and Server MUST support Write composite" | 1. Read `/1` and `/3` (S-preferred format). 2. iPATCH 257-SetOfValues: SenML JSON `[{"bn":"/","n":"1/0/2","v":101},{"n":"1/0/6","vb":true},{"n":"3/0/13","v":0}]`, or the given SenML CBOR hex `83 A3 21 61 2F 00 65 "1/0/2" 02 18 65 A2 00 65 "1/0/6" 04 F5 A2 00 66 "3/0/13" 02 00`. 3. Read again | 1. 2.05 with C.6 values. 2. 2.04. 3. 2.05 with the 257 values | DM-12, FMT-05, DT-01 (Time) | lwm2m::int_257 |
| 1.1-int-260 (6.3.31) | Discover | 1.1 (≥1.0) | S, C | C.4. No attributes on `/3` | 1. `GET /3` Acc 40. 2. WA `PUT /3?pmin=10&pmax=200`. 3. Discover `/3/0`. 4. WA `PUT /3/0/7?lt=1&gt=6&st=1`. 5. Discover `/3/0`. 6. Discover `/3/0/7` | 1. `</3>,</3/0>,</3/0/1>,</3/0/2>,</3/0/3>,</3/0/4>,</3/0/6>;dim=2,</3/0/7>;dim=2,</3/0/8>;dim=2,</3/0/9>,</3/0/11>,</3/0/16>`. 2. 2.04. 3. Same without `</3>`. 4. 2.04. 5. As 3 with `</3/0/7>;dim=2;lt=1;gt=6;st=1`. 6. Exactly `</3/0/7>;pmin=10;pmax=200;dim=2;lt=1;gt=6;st=1` (inherited pmin/pmax shown) | DM-04, DM-07, ATT-01, ATT-02, ATT-05, ATT-06 | lwm2m::int_260 |
| 1.1-int-261 (6.3.32) | Write-Attributes on multiple resource | 1.1 | S, C | C.1. 1.1. No attributes | 1. Discover `/3/0/11`. 2. WA `/3?pmin=10&pmax=200`. 3. WA `/3/0?pmax=320`. 4. WA `/3/0/11/0?pmax=100&epmin=1&epmax=20`. 5. Discover `/3/0/11` | 1. `</3/0/11>;dim=1,</3/0/11/0>`. 2-4. 2.04 each. 5. `</3/0/11>;dim=1;pmin=10;pmax=320,</3/0/11/0>;pmax=100&epmin=1&epmax=20` (`&` printed instead of `;`, **[ETS erratum]**) | DM-07, ATT-02, ATT-07, DM-04 | lwm2m::int_261 |
| 1.2-int-264 (6.3.33) | Discover depth 1 | 1.2 | S, C | C.4 with `/1/0/2`=1, `/1/0/3`=10. No attributes | 1. `GET /3?depth=1` Acc 40. 2. WA `/3/0?pmax=200`. 3. Discover `/3?depth=1` | 1. `</3>;pmin=1;pmax=10,</3/0>` (object level shows the server-default pmin/pmax). 2. 2.04. 3. `</3>;pmin=1;pmax=10,</3/0>;pmax=200` | DM-04 (Δ1.2 depth), ATT-02 | no |
| 1.2-int-266 (6.3.34) | Discover depth 3 | 1.2 | S, C | C.4. No attributes | 1. Discover `/3?depth=2`. 2. WA `/3?pmin=10&pmax=200`. 3. WA `/3/0/7?lt=1&gt=6&st=1`. 4. WA `/3/0/7/1?gt=8`. 5. Discover `/3?depth=3` | 1. `</3>;pmin=1;pmax=10,</3/0>,</3/0/1>,…,</3/0/6>;dim=2,</3/0/7>;dim=2,</3/0/8>;dim=2,</3/0/9>,</3/0/11>,</3/0/16>`. 2-4. 2.04. 5. `</3/0>;pmin=10;pmax=200,</3/0/1>,…,</3/0/6>;dim=2,</3/0/6/0>,</3/0/6/1>,</3/0/7>;dim=2;lt=1;gt=6;st=1,</3/0/7/0>,</3/0/7/1>;gt=8,</3/0/8>;dim=2,</3/0/8/0>,</3/0/8/1>,</3/0/9>,</3/0/11>,</3/0/16>` (no `</3>` line and attributes on `/3/0`, inconsistent with int-264, **[ETS erratum]**) | DM-04, DM-07, ATT-01 | no |
| 1.1-int-270 (6.3.35) | Create Object Instance | 1.1 | n/a | **Delegated** to int-1630 | none | int-1630 result | DM-09 | (portfolio::int_1630) |
| 1.1-int-271 (6.3.36) | Create Multiple Resource Instance | 1.1 | n/a | **Placeholder** (heading only) | none | none | DM-09 | no |
| 1.1-int-280 (6.3.37) | Successful Read-Composite | 1.1 | S, C | C.1. 1.1. Read-Composite supported | 1. FETCH with **exactly** `/3/0/16`, `/3/0/11/0`, `/1/0` | The client gets only those URIs. 2.05 SenML CBOR (112) or JSON (110): `/3/0/11/0`=0, `/3/0/16`="U", `/1/0/0`=1, `/1/0/1`=86400, `/1/0/6`=false, `/1/0/7`="U"; other `/1/0` resources MAY appear | DM-12, FMT-05 | lwm2m::int_280 |
| 1.1-int-281 (6.3.38) | Partially Successful Read-Composite | 1.1 | S, C | C.1. 1.1 | 1. FETCH `/1/0/1`, `/1/0/7`, `/1/0/8` | 2.05 containing only `/1/0/1`=86400 and `/1/0/7`="U". `/1/0/8` (executable) MUST NOT appear | DM-12, DM-03 | lwm2m::int_281 |
| 1.1-int-290 (6.3.39) | Delete Object Instance | 1.1 | n/a | **Delegated** to int-1635 | none | int-1635 result | DM-10 | (portfolio::int_1635) |

### 3.4 Information Reporting (§6.4, IDs 300-399)

| ID (§) | Title | Ver | Role | Pre / Cfg | Steps | Pass criteria | Req IDs | Zephyr |
|---|---|---|---|---|---|---|---|---|
| 1.1-int-301 (6.4.1) | Observation and Notification | 1.1 (≥1.0) | S, C | C.4. Registered | 1. `GET /3/0/6`. 2. WA `PUT /3/0/7?pmin=5&pmax=15` and `/3/0/8?pmin=10&pmax=20`. 3. `GET /3/0/7` and `/3/0/8` with Observe=0. 4. C notifies | S regularly receives the voltage and current values and displays them | OBS-01, OBS-04, ATT-03, ATT-04, DM-07 | lwm2m::int_301 (only `/3/0/7`, pmin 5 / pmax 10, timing asserted) |
| 1.1-int-302 (6.4.2) | Cancel Observations by Reset | 1.1 (≥1.0) | **S**, C | C.4. int-301 observations active | 1. Notifications on 7 and 8. 2. On a `/3/0/7` notify **S answers RST**. 3. Same for `/3/0/8`. 4. C stops | Notifications stop first for voltage, then current. C removes the observer entries | OBS-02, OBS-04 | lwm2m::int_302 |
| 1.1-int-303 (6.4.3) | Cancel with Observe=1 | 1.1 (≥1.0) | **S**, C | C.4. int-301 active | 1. Notifications. 2. S→C `GET /3/0` with **Observe=1** (cancel at instance level although the observations are on `/3/0/7` and `/3/0/8`; RFC 7641 §3.6 cancels per token and request options, **[ETS erratum]**). 3. C stops both | Both notification streams stop; entries removed | OBS-02 | lwm2m::int_303 (cancels per resource) |
| 1.1-int-304 (6.4.4) | Observe-Composite | 1.1 | S, C | C.1. No attributes or observations | 1. WA `PUT /1/x/1?pmin=30&pmax=45`. 2. FETCH Observe=0 on `/1/x/1`, `/3/0/11/y`, `/3/0/16` (CF and Acc SenML CBOR or JSON). 3. Wait 45 s | 1. 2.04. 2. 2.05 with all values. 3. A composite notification arrives no earlier than 30 s and no later than 45 s; 2.05 with an Observe option (printed "Observe = 1"); SenML; contains all three | OBS-05, ATT-02..04, DM-07 | lwm2m::int_304 |
| 1.1-int-305 (6.4.5) | Cancel Observe-Composite | 1.1 | **S**, C | C.1 | 1. FETCH Observe=0 as int-304. 2. FETCH **Observe=1 with the same path list** | 1. 2.05. 2. 2.05 with values; no notification afterwards | OBS-05, OBS-02 | lwm2m::int_305 |
| 1.1-int-306 (6.4.6) | Send | 1.1 | **S**, C | C.1. Client has periodic Send configured | 1. Wait for (or force) C→S `POST /dp` | Send carries only objects registered at Register. CF 112 or 110. **S accepts with 2.04** | SEND-01, SEND-02, FMT-05 | lwm2m::int_306 |
| 1.1-int-307 (6.4.7) | Muting Send | 1.1 | S, C | C.1. `/1/x/23` Mute Send present | 1. Observe a Send. 2. Write `/1/x/23` = true "CoAP POST" (**[ETS erratum]**, PUT). 3. Trigger Send. 4. `PUT /1/x/23` = false. 5. Trigger Send | 1. Send accepted, 2.04. 2. 2.04. 3. No Send reaches S. 4. 2.04. 5. Send arrives | SEND-01, SEND-02 | lwm2m::int_307 |
| 1.1-int-308 (6.4.8) | Observe-Composite + Create | 1.1 | S, C | C.1 + Portfolio. No `/16/1` | 1. Create `POST /16` instance 0 with Identity `/16/0/0/0..3` = aa, bb, cc, dd. 2. WA `/16/0?pmin=30&pmax=45`. 3. FETCH Observe=0 `/16/0`, `/16/1`. 4. Wait 45 s. 5. Create instance 1 with 11, 22, 33, 44 (paths printed as `/16/0/0/x`, **[ETS erratum]**). 6. Wait 45 s | 1. 2.01. 2. 2.04. 3. 2.05 with only `/16/0`. 4. Notification at 30-45 s with only inst 0. 5. 2.01. 6. Notification 30-45 s after the previous one with inst 0 **and** inst 1 | OBS-05, DM-09, ATT-03/04 | lwm2m::int_308 |
| 1.1-int-309 (6.4.9) | Observe-Composite + Delete | 1.1 | S, C | C.1 + Portfolio | 1. Create `/16/0` (aa..dd). 2. Create `/16/1` (11..44). 3. WA `/16/0?pmin=30&pmax=45`. 4. FETCH Observe=0 `/16/0`, `/16/1`. 5. Wait 45 s. 6. `DELETE /16/1`. 7. Wait 45 s | 1-2. 2.01. 3. 2.04. 4. 2.05 with both. 5. Notification with both at 30-45 s. 6. "2.04 Deleted" (sic, 2.02). 7. Notification with inst 0 only | OBS-05, DM-10 | lwm2m::int_309 |
| 1.1-int-310 (6.4.10) | Observe-Composite + attribute change | 1.1 | S, C | C.1. 1.1. Observe-Composite supported | 1. FETCH observe (printed "Observe Option set to 1", **[ETS erratum]**, means 0) on `/1/0/3` (text calls it "Lifetime (ID:3)") and `/3/0` (pass says `/3`). 2. Notify. 3. WA `PUT /3?pmax=5`. 4. Notifications every 5 s | 1. FETCH carries only those URIs; 2.05 with values. 2. Notify. 3. 2.04. 4. Regular notifications | OBS-05, DM-07, ATT-04 | lwm2m::int_310 (uses `/1/0/1`) |
| 1.1-int-311 (6.4.11) | Send command | 1.1 | **S**, C | C.1. 1.1. Send supported by both | 1. C→S `POST /dp` with `/1/0/1`=86400, `/3/0/11/0`=0 | S got the expected values. C got 2.04 | SEND-01, SEND-02 | lwm2m::int_311 |
| 1.2-int-312 (6.4.12) | Observe with attributes as parameters | 1.2 | S, C | C.4 | 1. S→C **`GET /3/0/7?pmin=5&pmax=15`** Observe=0. 2. Notifications | 1. Response holds `/3/0/7/0` and `/3/0/7/1`. 2. Notifications at least every 15 s | OBS-06, OBS-01, ATT-04 | no |
| 1.2-int-313 (6.4.13) | Observe parameters override attributes | 1.2 | S, C | C.4 | 1. WA `PUT /3/0/7?pmin=5&pmax=60`. 2. `GET /3/0/7?pmin=5&pmax=10` Observe=0. 3. Notifications | 1. 2.04. 2. Both instances. 3. **At least every 10 s** (the Observe parameters win over the attached pmax 60) | OBS-06, ATT-02 | no |

### 3.5 Security (§6.5, IDs 400-499)

| ID (§) | Title | Ver | Role | Pre / Cfg | Steps | Pass criteria | Req IDs | Zephyr |
|---|---|---|---|---|---|---|---|---|
| 1.1-int-401 (6.5.1) | UDP DTLS PSK | 1.1 (≥1.0) | S, C | C.1. `/0/0/2`=0 | 1. DTLS PSK handshake. 2. `POST /rd`. 3. 2.01. 4. S→C `GET /3/0` TLV (int-203). 5. 2.05 | Register and Read succeed over DTLS | SEC-01..06, SEC-11, REG-17 | bootstrap `verify_…_int_401` (checks `secure`) |
| 1.1-int-402 (6.5.2) | UDP DTLS Certificate | 1.1 (heading says "1.0-int-402") | S, C | C.17. DNS: `server.example.com` points to S | Same 5 steps with cert mode | Register and Read succeed over DTLS | SEC-03, SEC-10, SEC-06 | no |
| 1.1-int-403 (6.5.3) | Certificate, server identity failure | 1.1 | C (S presents its cert) | C.18. `server-fail.example.com` points to S | DTLS handshake; the S cert is for `server.example.com`, the URI says `server-fail` | **Handshake fails** (client-side hostname check). S only has to present the correct cert | SEC-10, SEC-14 | no |
| 1.1-int-404 (6.5.4) | TCP TLS Certificate | 1.1 | C, BS, S | C.19 (cert, `coaps+tcp`) | 1-3. TCP, then TLS (cert), then CSM with the **BS**. 4. Bootstrap. 5-6. TCP + TLS to S. 7-8. `POST /rd`. 9. S→C `GET /3/0` | 1-3. TCP, TLS and CSM OK. 4. Register has b=T, link-format list without `/0` and `/21`; 2.01. Read 2.05, CF chosen by the client, over TLS | SEC-03, SEC-10, REG-18, BS-10 (RFC 8323) | no |
| 1.1-int-405 (6.5.5) | OSCORE | 1.1 | S, C | "C.19" as printed (**[ETS erratum]**: the OSCORE config is C.22) | 1. UDP or TCP. 2. OSCORE-protected `POST /rd`. 3. S→C `GET /3/0` | 1. **Every message carries the OSCORE option; outer code is POST or 2.04; the payload is encrypted.** 2. Register params as int-101, 2.01. 3. Read returns `/3/0` | SEC-15, GEN-07 (Echo), REG-01 | no |
| 1.1-int-406 (6.5.6) | TCP TLS PSK | 1.1 | C, BS, S | C.20 (PSK, b=T) | As int-404 with PSK | As int-404 with PSK | SEC-04, SEC-05, REG-18 | no |

### 3.6 Core objects (§6.6, IDs 500-999)

| ID (§) | Title | Ver | Role | Pre / Cfg | Steps | Pass criteria | Req IDs | Zephyr |
|---|---|---|---|---|---|---|---|---|
| (6.6.1) | Security Object [500-549] | | | **No cases** | | | | |
| 1.1-int-551 (6.6.2.1) | Access Check to the Resources | 1.1 | n/a | **Delegated**: status equals int-215 | none | none | | (int_215) |
| 1.1-int-555 (6.6.2.2) | Disable (De-Registration) capability | 1.1 | n/a | **Placeholder**; "int-103 partially addresses" it | none | none | REG-16 | no |
| 1.1-int-556 (6.6.2.3) | Update Registration capability | 1.1 | n/a | **Delegated**: status equals int-104 | none | none | | (int_104) |
| 1.1-int-560 (6.6.2.4) | Delayed Report Notification | 1.1 | n/a | **Placeholder** | none | none | (ATT-08 hqmax, /1/x/6) | no |
| 1.1-int-565 (6.6.2.5) | Create Object Instance (/1) | 1.1 | n/a | **Placeholder** | none | none | | no |
| 1.1-int-566 (6.6.2.6) | Delete Object Instance (/1) | 1.1 | n/a | **Placeholder** | none | none | | no |
| (6.6.3) | Access Control Object [600-649] | | | **No cases** | | | | |
| 1.1-int-651 (6.6.4.1) | Device: Check Access to Resources | 1.1 (≥1.0) | S, C | C.6. S saved initial `/3/0` | 1. `POST /3/0` TLV 651-SetOfValues: `C4 0D 51 82 42 8F` (/3/0/13=1367491215), `C6 0E 2B 30 32 3A 30 30` (/3/0/14="+02:00"), `C8 0F 0C 45 75 72 6F 70 65 2F 50 61 72 69 73` (/3/0/15="Europe/Paris"; the JSON form says "[Europe/Paris]"). 2. `GET /3/0`. 3. `PUT /3/0` with the saved values. 4. `GET /3/0` | 2.04, 2.05, 2.04, 2.05. Values match 651 ("step 3"), then the initial values ("step 7, C.3", **[ETS erratum]**) | DM-05, FMT-04, DT-01 | no |
| 1.1-int-652 (6.6.4.2) | Query firmware version | 1.1 (≥1.0) | S, C | C.6 | 1. `GET /3/0/3` | 2.05, client-preferred format | DM-02 | no |
| 1.1-int-655 (6.6.4.3) | Reboot capability | 1.1 | n/a | **Delegated** = int-241 | none | none | | (int_241) |
| 1.1-int-656 (6.6.4.4) | Factory Reset | 1.1 | n/a | **Placeholder** (only if the client can bootstrap, Core §5.2.3) | none | none | | no |
| 1.1-int-657 (6.6.4.5) | Error Code functionality | 1.1 | n/a | **Placeholder** | none | none | | no |
| 1.1-int-660 (6.6.4.6) | Basic Observation of Device | 1.1 | n/a | **Delegated** = int-301 | none | none | | (int_301) |
| 1.1-int-661 (6.6.4.7) | Extended Observation of Device | 1.1 | n/a | **Placeholder** | none | none | | no |
| 1.1-int-670 (6.6.4.8) | Device: Create Multiple Resource Instances | 1.1 | n/a | **Placeholder** | none | none | | no |
| 1.1-int-680 (6.6.4.9) | Device: Create Object Instance | 1.1 (≥1.0) | C, S | C.1 | 1. `POST /3` (Create) | **4.05 Method Not Allowed** | DM-09, GEN-06 | no |
| 1.1-int-685 (6.6.4.10) | Device: Delete Object Instance | 1.1 (≥1.0) | C, S | C.1 | 1. `DELETE /3/0` | **4.05** | DM-10 | no |
| 1.1-int-701 (6.6.5.1) | Conn. Monitoring: read | 1.1 (≥1.0) | S, C | C.5 | 1. `GET /4/0` | 2.05 with mandatory 0, 1, 2, 4 plus optional resources, values per TS | DM-02, OBJ (/4) | no |
| 1.1-int-705 (6.6.5.2) | Conn. Monitoring: set writable | 1.1 | n/a | "There is no writable resources" | none | none | | no |
| 1.1-int-710 (6.6.5.3) | Conn. Monitoring: basic observation | 1.1 (≥1.0) | S, C | C.5 | 1. WA `PUT /4/0?pmin=2&pmax=10`. 2. Observe `/4/0`. 3. Notify | WA received and 2.04. Observe 2.05 with initial values. Notifications per pmin/pmax with updated values | OBS-01, OBS-04, DM-07 | no |
| 1.1-int-711, 720, 730, 735 (6.6.5.4-7) | Conn. Mon. extended observation / create MRI / create / delete | 1.1 | n/a | **Placeholders** | none | none | | no |
| 1.1-int-751 (6.6.6.1) | FW: query readable resources | 1.1 (≥1.0) | S, C | C.8. State=0 | 1. `GET /5/0` Acc TLV (step printed "4.") | 2.05. State (3) = 0 and Update Result (5) = 0. `/5/0/8` and `/5/0/9` reveal the supported protocols and delivery methods | DM-02, FW-03, FW-04 | no |
| 1.1-int-755 (6.6.6.2) | FW: write Package | 1.1 (≥1.0) | S, C | C.8. Push supported (from 751) | 1. `PUT /5/0/0` = `'\0'`. 2. `GET /5/0` (State, Result). 3. `PUT /5/0/0` with a valid image. 4. `GET /5/0` | 1. 2.04. 2-3. 2.05 with State 0, Result 0. 4. 2.04. 5-6. State **2** (Downloaded), Result 0 | FW-01, FW-02, FW-04 | no (blockwise_1/2 push `/5/0/0` but are not ETS-mapped) |
| 1.1-int-756 (6.6.6.3) | FW: write Package URI | 1.1 (≥1.0) | S, C | C.8. Pull supported | 1. Write the empty string (text says "Package Resource (ID:0)" and description says URI (ID:1), **[ETS erratum]**). 2. `GET /5/0`. 3. Write a valid URI ("valid image" printed). 4. `GET /5/0` | As int-755: 0/0, then State 2, Result 0 | FW-02, FW-03, FW-04 | no |
| 1.1-int-760 (6.6.6.4) | FW: observe State | 1.1 (≥1.0) | S, C | C.8. Push. int-755 passed. State Idle | 1. WA `PUT /5/0?pmin=2&pmax=10`. 2. Observe `/5/0/3`. 3. `PUT /5/0/0` image. 4. Notify | 1. 2.04. 2. 2.05 State=Idle. 3. 2.04. 4. Notification with State = Downloaded | OBS-01, FW-01, FW-04 | no |
| 1.1-int-770 (6.6.6.5) | Successful FW update via CoAP (push) | 1.1 (≥1.0) | S, C | C.8 (pre says "PULL", **[ETS erratum]**: the flow is push). int-755 passed. State 0 or "1 (Downloaded)" | 1a. `PUT /5/0/0` = `'\0'`. 1b. `PUT /5/0/0` image (Block1). 1c. Poll or observe State and Result until State=2. 2a. `POST /5/0/2`. 2b. Poll until State=0 or Result≠0. 3a. Read `/5/0/5`, `/5/0/3`. 3b. Read `/3/0/3` | 1a. 2.04. 1b. **2.31 Continue or final 2.04**. 1c. State 1 then 2. 1d. Result 0 throughout. 2a. 2.04. 2b. State 3 or 0, Result 0 or 1. 3a. State 0, Result 1. 3b. New FW version | FW-01, FW-02, FW-04, FW-05 (RFC 7959 Block1) | no |
| 1.1-int-771 (6.6.6.6) | Successful FW update via URI (pull) | 1.1 (≥1.0) | S, C (+download server) | C.8 (pre says "PUSH", swapped, **[ETS erratum]**) | 1a. `PUT /5/0/1` = "". 1b. `PUT /5/0/1` = URI. 1c. C downloads via an "alternative mechanism (not CoAP)". 1d. Poll until State=2. 2-3. As int-770 | 1a-1b. 2.04. 1c. "2.31 or 2.04". 1d. State 1 then 2; Result 0. 2-3. As int-770 | FW-02, FW-03, FW-04 | no |
| 1.1-int-772 (6.6.6.7) | FW error 1: install without package | 1.1 (≥1.0) | C, S | C.8. State ≠ 2 | 1. `GET /5/0`. 2. `POST /5/0/2`. 3. `GET /5/0/3` | 1. 2.05, State≠2, Result 0-9. 2. **4.05**. 3. Same State and Result as before | FW-04, DM-08 | no |
| 1.1-int-773 (6.6.6.8) | FW error 2: storage shortage | 1.1 (≥1.0) | C, S | C.8. Oversized package. State 0 | 1. `GET /5/0/3`. 2. Push `/5/0/0` or pull `/5/0/1`. 3. Poll or observe `/5/0`. 4. Read Result | 1. 0. 2. 2.04. 3. State 1. 4. **Result 2**; "never reaches Downloaded ("3")" (sic). 5. State 0, Result 2 | FW-04 | no |
| 1.1-int-774 (6.6.6.9) | FW error 3: out of RAM | 1.1 (≥1.0) | C, S | C.8. Package that exhausts RAM | as int-773 | As int-773 with **Result "2"** (**[ETS erratum]**: Object 5 defines 3 = out of RAM) | FW-04 | no |
| 1.1-int-775 (6.6.6.10) | FW error 4: connection lost (URI) | 1.1 (≥1.0) | C, S (+download server) | C.8. A way to cut the connection | Pull via `/5/0/1`; cut the link during download; poll; read | State 1, then **Result 4**; final State 0, Result 4 | FW-04 | no |
| 1.1-int-776 (6.6.6.11) | FW error 5: integrity failure | 1.1 (≥1.0) | C, S | C.8. Corrupt package | Push or pull; poll; read | State 1, then **Result 5**; final State 0, Result 5 | FW-04 | no |
| 1.1-int-777 (6.6.6.12) | FW error 6: unsupported package type | 1.1 (≥1.0) | C, S | C.8 | Push or pull; poll; read | State 1, **Result 6**; final "State 1, Result 6" (differs from 773-776) | FW-04 | no |
| 1.1-int-778 (6.6.6.13) | FW error 7: invalid URI | 1.1 (≥1.0) | C, S | C.8. Pull. Fake URI | `GET /5/0/3`; write the bad URI to `/5/0/1`; poll; read | 2.04 for the write; State 1, **Result 7**; final State 0, Result 7 | FW-02, FW-04 | no |
| 1.1-int-779 (6.6.6.14) | FW error 8: install failure | 1.1 (≥1.0) | C, S | C.8. Package downloads but fails to install | 1a. `GET /5/0/3`=0. 1b. `GET /3/0/3`. 1c. Push or pull. 1d. Poll until State 2. 2a. `POST /5/0/2`. 2b. Poll until State 2 or Result 8. 3a. Read State and Result. 3b. Read `/3/0/3` | 1a-d. 0; FW version; 2.04; State 2. 2a. 2.04. 2b. State 3 or 2, Result 0 then 8. 3a. State 2, **Result 8**. 3b. FW version unchanged | FW-04 | no |
| 1.1-int-780 (6.6.6.15) | FW error 9: unsupported protocol | 1.1 | n/a | **Placeholder** ("paragraph") | none | none | FW-03 | no |
| 1.1-int-801 (6.6.7.1) | Location: read | 1.1 (≥1.0) | S, C | C.7 | 1. `GET /6/0` | 2.05 with Latitude, Longitude, Timestamp and the optional resources | DM-02 | no |
| 1.1-int-805 (6.6.7.2) | Location: set writable | 1.1 | n/a | "There are no writable resources" | none | none | | no |
| 1.1-int-810 (6.6.7.3) | Location: basic observation | 1.1 (≥1.0) | S, C | C.7 | 1. WA `PUT /6/0?pmin=2&pmax=10`. 2. Observe `/6/0`. 3. Notify | 2.04. 2.05 initial. Notifications per pmin/pmax; Timestamp admissible | OBS-01, DM-07, ATT-03/04 | no |
| 1.1-int-811, 820, 830, 835 (6.6.7.4-7) | Location extended / MRI / create / delete | 1.1 | n/a | **Placeholders** | none | none | | no |
| 1.1-int-901 (6.6.8.1) | Conn. Statistics: data collection | 1.1 (≥1.0) | S, C | C.9 | 1. `POST /7/0/6` Start. 2. After a few s `POST /7/0/7` Stop. 3. `GET /7/0` | Exec 2.04 ×2 (printed "2.4"). Read 2.05, client format, resources 0-5 coherent | DM-08, DM-02 | no |
| 1.1-int-905 (6.6.8.2) | Conn. Statistics: set writable | 1.1 (≥1.0) | S, C | C.9 | 1. `GET /7/0`. 2. `PUT /7/0/8` (new Collection Period). 3. `GET /7/0` | 2.05, 2.04, 2.05; `/7/0/8` = written value | DM-05, DM-02 | no |
| 1.1-int-910 (6.6.8.3) | Conn. Statistics: observation | 1.1 (≥1.0) | S, C | C.9 | 1. WA `/7/0?pmin=2&pmax=10`. 2. `PUT /7/0/8`=0. 3. Observe `/7/0`. 4. `POST /7/0/6`. 5-6. Notifications. 7. `POST /7/0/7`. 8. Stop observing | WA 2.04. Observe 2.05 initial. Notifications per pmin/pmax with updated stats | OBS-01, OBS-02, DM-07, DM-08 | no |
| 1.1-int-911, 920, 930, 935 (6.6.8.4-7) | Conn. Stats extended / MRI / create / delete | 1.1 | n/a | **Placeholders** | none | none | | no |
| 1.1-int-950 (6.6.9.1) | Multi-Servers Registration | 1.1 (≥1.0) | S×2, C | C.14 (2 server accounts, ACL) | 1. C registers to S#1. 2. C registers to S#2 | Each server receives a Register for its own account (int-101 rules), 2.01 | REG-01, REG-05, DM-14 | no |
| 1.1-int-951 (6.6.9.2) | Multi-Servers & Attributes | 1.1 (≥1.0) | S×2, C | C.14. int-950 passed | 1. S#1 WA `PUT /3/0?pmin=2&pmax=10`. 2. S#2 WA `PUT /3/0?pmin=15&pmax=50`. 3. S#1 Discover `/3/0`. 4. S#2 Discover `/3/0` | 1-2. 2.04. 3. `</3/0>;pmin=2;pmax=10,</3/0/0>,</3/0/1>,</3/0/2>,</3/0/3>,</3/0/11>,</3/0/16>`. 4. Same with pmin=15, pmax=50 (attributes are per server) | DM-04, DM-07, ATT-01 | no |

### 3.7 Additional objects (§6.7, IDs 1000-2099)

| ID (§) | Title | Ver | Role | Pre / Cfg | Steps | Pass criteria | Req IDs | Zephyr |
|---|---|---|---|---|---|---|---|---|
| (6.7.1, 6.7.2) | Lock and Wipe (8), Software Mgmt (9) | | | **No cases** ("paragraph") | | | | |
| 1.1-int-1200 (6.7.3.1.1) | Cellular Connectivity /10 read | 1.1 | S, C | C.10 | 1. `GET /10/0` Acc TLV | 2.05 TLV consistent with C.10 (PSM timer, Active timer, eDRX WB-S1 and NB-S1, Serving PLMN rate control) | DM-02, FMT-04, VER-01 | no |
| 1.1-int-1201 (6.7.3.1.2) | /10 v1.1 read | 1.1 | S, C | C.11. Registered with `</10>;ver=1.1` | 1. Discover `/10`. 2. `GET /10/0` TLV | 1. `</10>;ver="1.1",</10/0>,</10/0/4>,</10/0/5>,</10/0/6>,</10/0/8>,</10/0/9>,</10/0/11>,</10/0/13>,</10/0/14>`. 2-3. 2.05 TLV consistent (adds APN list, PSM modes) | VER-01, VER-02, DM-04 | no |
| 1.1-int-1202 (6.7.3.1.3) | /10 v1.1 set PSM | 1.1 | S, C | C.11. `/10/0/13` ≠ 0 | 1. Discover `/10`. 2. `GET /10/0/13`. 3. `PUT` or `POST /10/0/14` TLV 1210-SetOfValues (`/10/0/14`=2; TLV printed `C10 0D 02`, **[ETS erratum]**: should be `C1 0E 02`). 4. `GET /10/0/14` | 1. Discover lists `</10>;ver="1.1",</10/0>,</10/0/6>,</10/0/11>,</10/0/13>,</10/0/14>`. 2. Value matches C.11 (3). 3. 2.04. 4. 2 | DM-05, FMT-04, VER-01 | no |
| 1.1-int-1203 (6.7.3.1.4) | /10 v1.1 observe PSM | 1.1 | S, C | C.11 | 1. Discover. 2. WA `/10/0/13?pmin=5&pmax=15`, `/10/0/14?pmin=10&pmax=20`. 3. Observe both. 4. Notify | Discover as 1202. 2.04 ×2. 2.05 initial. Regular notifications | OBS-01, DM-07 | no |
| 1.1-int-1204 (6.7.3.1.5) | /10 observe timers | 1.1 | S, C | C.10 | 1. Discover `/10`. 2. WA `/10/0/4?pmin=5&pmax=15`, `/10/0/5?pmin=10&pmax=20`, `/10/0/8?pmin=5&pmax=15`, `/10/0/9?pmin=10&pmax=20`. 3. Observe (text lists "ID:4, 5, 9, 10"; pass lists 4, 5, 8, 9). 4. Notify | Discover `</10>;ver="1.1",</10/0>,</10/0/4>,</10/0/5>,</10/0/6>,</10/0/8>,</10/0/9>`. 2.04s. 2.05 initial. Regular notifications | OBS-01, DM-07 | no |
| CONMGMT-1.1-int-1250 (6.7.3.2.1) | APN configuration | 1.1 | S, C | C.10. int-104 passed. Cellular up with `/11/0` | 1. Create `POST /11` (second APN, not active). 2. S executes the Registration Update Trigger. 3. C→S Update with an object list containing `/11/1`. 4. `PUT /11/1/3` = true. 5. `GET /10/0/11` TLV | 1. 2.01. 2. Trigger received ("Resource ID:7 of the Device Object", **[ETS erratum]**, means `/1/x/8`); 2.04. 3. **S stores the updated object list with `/11/1`**. 4. 2.04. 5. 2.05 list contains `11:1` | DM-09, REG-14, REG-15, REG-19 | no |
| CONMGMT-1.1-int-1350 (6.7.3.4.1) | Bearer Selection | 1.1 | S, C | C.10. `/10`, one `/11`, `/12` present with WLAN off | 1. Create `/13` with `/13/0/0` = WLAN preferred. 2. 2.01. 3. C switches to WLAN and **sends an Update (new IP/port)**. 4. `GET /12/0` (Enable, Status). 5. `PUT /13/0/0` = 3GPP PS preferred. 6. C switches to cellular and sends an Update (new address). 7. `GET /10` | "Bearer Selection Object is allowing the Server to control Client interface for communication" | DM-09, REG-11, REG-14, REG-17 | no |
| (6.7.4) | Device Capability Mgmt /15 [1500-1599] | | | **Placeholder** | | | | |
| 1.1-int-1600, 1605, 1610, 1611, 1620 (6.7.5.1-5) | Portfolio read / write / basic observation / extended observation / MRI | 1.1 | n/a | **Placeholders** | none | none | | no |
| 1.1-int-1630 (6.7.5.6) | Create Portfolio Instance | 1.1 (≥1.0) | S, C | C.12 | 1. Discover `/16/0`. 2. Create `POST /16` TLV 1630-SetOfValues: `08 01 2E` (instance 1), `80 00 2B` (multiple resource 0), `48 00 11 "Host Device ID #2"`, `48 01 14 "Host Device Model #2"`. 3. Discover `/16/1`. 4. `GET /16` | 1. `</16/0/0/>;dim=4`. 2. 2.01 with the new instance (SHOULD be `/16/1`). 3. `</16/1/0/>;dim=2`. 4. 2.05 TLV with inst 0 (4 Identity instances, C.12) and inst 1 (2 instances) | DM-09, DM-04, FMT-04 | portfolio::int_1630 (uses SenML CBOR, not TLV) |
| 1.1-int-1635 (6.7.5.7) | Delete all Portfolio Instances | 1.1 (≥1.0) | S, C | C.12 | 1. Discover `/16/0`. 2. `DELETE` each `/16/x`. 3. Discover `/16` | 1. At least `</16/0>,</16/0/0/>;dim=4`. 2. "2.04 Deleted" (sic, 2.02) each. 3. `</16>` only (object present, no instances) | DM-10, DM-04 | portfolio::int_1635 |
| 1.1-int-1900 (6.7.6.1) | BinaryAppData /19 observe | 1.1 | S, C | C.15 | 1. Discover `/19`. 2. WA `/19/0/0?pmin=300&pmax=3600`. 3. Observe `/19/0/0`. 4. Notify | 1. `</19>;ver="1.1",</19/0>,</19/0/0>` (Object 19 latest is 1.0 in the registry, standards.md §4, **[ETS erratum]**). 2. 2.04. 3. 2.05 initial. 4. Regular notifications | OBS-01, DM-07, VER-01 | no |
| 1.1-int-1901 (6.7.6.2) | /19 write Data | 1.1 | S, C | C.15 | 1. `PUT /19/1/0`. 2. `GET /19/1/0` | 2.04. 2.05 with the same value | DM-05, FMT-01 (opaque) | no |
| 1.1-int-2000 (6.7.7.1) | Event Log: LogStart | 1.1 | S, C | C.16 | 1. `PUT /20/0/4011`. 2. `GET /20/0/4011` | 2.04. 2.05 with the same value | DM-05 | no |
| 1.1-int-2001 (6.7.7.2) | Event Log: read | 1.1 | S, C | C.16 | 1. `GET /20/0` TLV (description copy-pasted from FW, **[ETS erratum]**) | 2.05 TLV consistent with C.16 (4013, 4010, 4014) | DM-02, FMT-04 | no |

---

## 4. Summary counts

There are 155 test IDs in ETS INT 1.2 §6 (the headings include int-11 under §6.1.2): 118 have a full procedure ("Test Case Id" block); 37 do not (6 delegated, 2 "no writable resources", 29 placeholders).

| Group (§) | IDs | With procedure | Delegated | N/A | Placeholder | Of which 1.2-int | Zephyr-automated |
|---|---|---|---|---|---|---|---|
| 6.1 Bootstrap | 14 | 14 | 0 | 0 | 0 | 3 (11, 12, 19) | 6: int-0, 1, 4, 5, 6, 7 |
| 6.2 Registration | 11 | 11 | 0 | 0 | 0 | 2 (110, 111) | 8: 101-105, 107-109 |
| 6.3 DM & SE | 39 | 34 | 2 (270, 290) | 0 | 3 (202, 210, 271) | 2 (264, 266) | 32: 201, 203-205, 211, 212, 215, 220-237, 241, 256, 257, 260, 261, 280, 281 |
| 6.4 Info Reporting | 13 | 13 | 0 | 0 | 0 | 2 (312, 313) | 11: 301-311 |
| 6.5 Security | 6 | 6 | 0 | 0 | 0 | 0 | 1: 401 (helper) |
| 6.6 Core objects | 54 | 27 | 4 (551, 556, 655, 660) | 2 (705, 805) | 21 | 0 | 0 |
| 6.7 Additional objects | 18 | 13 | 0 | 0 | 5 (1600-1620) | 0 | 2: 1630, 1635 |
| **Total** | **155** | **118** | 6 | 2 | 29 | **9** | **60** |

By ID version label: `1.2-int` = 9; `1.1-int` = 143; `1.0-int` = 1 (int-402: heading says "1.0", its Test Case Id says 1.1); `CONMGMT-1.1-int` = 2 (1250, 1350).
- 1.1-only: Discover in bootstrap (int-0 step 3), CBOR, SenML, composite, Send, epmin/epmax, OSCORE, TCP, EST;
- 1.2: the nine 1.2-int cases.

1.2.1-C: same 155 IDs. int-229 is voided, which leaves **117** cases with a procedure. Preconditions change for int-1 and int-2. int-8 now has a defined value set.

TestFest entry criteria (App. E.1): int-101, 102, 201, 203.

---

## 5. Cases that test SERVER behaviour specifically

Almost every case is written as "Server performs X, Client replies Y", so the pass criteria judge the client. Our server is then the test-driving peer. The cases below have a pass criterion **about the Server or BS itself**:

| Case | Server/BS behaviour judged |
|---|---|
| int-0, 1, 2, 4, 8, 9, 10, 11 | BS answers `/bs` with 2.04, runs the Write/Discover/Read/Delete/Finish sequence, and handles the client's 4.06 on Finish (int-9) |
| int-12 | BS answers `GET /bspack` with a 2.05 pack (SenML or LwM2M CBOR) (BS-12) |
| int-19 | BS over MQTT: subscribe `+/lwm2m/bs/+`, publish ops (out of scope for a CoAP-only server unless MQTT transport is added) |
| int-101, 106, 404, 405, 406, 950 | S accepts Register (UDP, TCP+CSM, TLS, OSCORE, two servers) with 2.01 + Location |
| int-102 | S de-registers the client when the 20 s lifetime expires without an Update |
| int-103 | S answers De-register with 2.02 and removes the client from its DB |
| int-104 | S refreshes the lifetime on a parameterless Update (client still registered after the initial lifetime) |
| **int-105** | **S answers 4.04 to an Update for a registration it removed**; accepts the re-Register |
| int-107, 226, 227, 230, 233, 234 | S accepts an Update with a changed `lt` (2.04) and stores it |
| **int-108** | **S marks the client as Queue Mode from `Q`** |
| **int-109** | **S queues a downlink while the client sleeps and sends it only after the next Update** |
| **int-110, 111** | **S resolves `pid=6:27354b9a` (32-bit truncated SHA-256) to the C.1 object list, merges it with any explicit list, answers 2.01** |
| int-302 | S answers RST to a notification for an observation it cancelled |
| int-303, 305 | S issues a GET or FETCH with Observe=1 carrying the same token and options (path list for composite) |
| int-306, 307, 311 | S accepts `POST /dp` with 2.04 (SEND-01/02) |
| int-1250, 1350 | S accepts an Update with a new object list or from a new address and keeps the registration |
| int-403 | S presents a cert for `server.example.com` (the failure itself is client-side) |
| int-951 | S keeps its attributes separate from another server's (client-side, but needs two independent servers) |

### 5.1 Server capabilities implicitly needed to drive the client-side cases

| Capability | Needed by | Req IDs |
|---|---|---|
| Register/Update/De-register endpoint; ep, lt, lwm2m 1.0/1.1/1.2, b (U, UQ, T, M), Q, pid; lifetime expiry | 101-111, 241, 950, 1250, 1350 | REG-01..22, QM-01 |
| Operator API to issue any operation on demand and show the result (B.1 "GUI") | all | none |
| Read with explicit Accept 0/42/60/110/112/11542/11543 and **without Accept**; decode every response format | 201-237, 280, 281, 651, 652, 701, 751, 801, 901, 1200, 2001 | DM-02, FMT-01..05, DT-02 |
| Write PUT (resource, resource instance, instance replace) and POST (partial update) in plain text, CBOR, TLV, JSON, SenML JSON, SenML CBOR; **TLV/JSON/SenML encoders producing the exact sample bytes** (215, 220, 257, 651, 1210, 1630) | 205-234, 256, 651, 755, 756, 905, 1202, 1901, 2000 | DM-05, DM-06, FMT-04, FMT-05 |
| Write a NUL byte `'\0'` to `/5/0/0` and an empty string to `/5/0/1` | 755, 756, 770, 771 | FW-04 |
| Block1 push of large Package (accept 2.31) and a host for pull URIs (CoAP block2 or HTTP) | 755, 760, 770-779 | FW-01, FW-02, GEN-04 |
| Execute without args (`/1/x/4`, `/1/x/8`, `/1/x/9`, `/3/0/4`, `/5/0/2`, `/7/0/6`, `/7/0/7`) | 5, 103, 104, 241, 772, 901, 910, 1250 | DM-08, REG-19, BS-11 |
| Create (TLV and SenML) on `/16`, `/11`, `/13`; expect 4.05 on `/3` | 228, 308, 309, 1250, 1350, 1630, 680 | DM-09 |
| Delete instance; expect 4.05 on `/3/0` | 309, 1635, 685 | DM-10 |
| Discover with link-format parsing (dim, pmin, pmax, gt, lt, st, epmin, epmax, ver) and **`?depth=`** | 260, 261, 264, 266, 951, 1201-1204, 1630, 1635, 1900 | DM-04, ATT-01 |
| Write-Attributes incl. on `/0` (expect 4.01), multi-level, resource-instance level, epmin/epmax | 221, 260, 261, 264, 266, 301, 304, 308-310, 313, 710, 760, 810, 910, 951, 1203, 1204, 1900 | DM-07, ATT-01..07 |
| Observe, notification matching, RST on unknown token, active cancel (Observe=1) | 301-303, 312, 313, 710, 760, 810, 910, 1203, 1204, 1900 | OBS-01..04 |
| **Observe with attribute query params** (`GET /3/0/7?pmin=5&pmax=15`, Observe=0) | 312, 313 | OBS-06 |
| Read-/Write-/Observe-Composite (FETCH/iPATCH; SenML 110/112 bodies; 1.2 clients may need SenML-ETCH 320/322); cancel with an identical path list | 229, 230, 235, 236, 257, 280, 281, 304, 305, 308-310 | DM-12, OBS-05 |
| `/dp` Send handler (SenML JSON/CBOR, 1.2 also LwM2M CBOR 11544) | 306, 307, 311 | SEND-01, SEND-02 |
| Queue mode: hold requests, send after an uplink | 109 | QM-01..04 |
| DTLS 1.2 PSK; DTLS X.509 with a cert for `server.example.com`; TLS over TCP (RFC 8323 CSM) with PSK and cert; OSCORE + Echo | 401-406, 1, 2 | SEC-01..15 |
| BS: Write (object-level `/0`, `/1`, `/2`, `/21`), Discover `/`, Read `/1` and `/2`, Delete `/0` and `/1`, Finish (handling 4.06), Pack-Request, EST enrolment, OSCORE BS | 0-12 | BS-01..13 |
| Second independent server instance (SSID 2) | 950, 951 | none |
| Profile ID registry (SHA-256 of a canonical object list, truncated to 32 bits, prefix `6:`) | 110, 111 | REG-21 |

---

## 6. Running the ETS against our server

### 6.1 What a harness needs

1. **Server operator API.** One call per LwM2M operation (Read with or without Accept, Write PUT/POST with a chosen CF, Write-Attributes, Discover with depth, Execute, Create, Delete, Observe with params, cancel active/passive, composite read/write/observe, BS config). It returns status code, CF and decoded payload, plus an event stream for Register, Update, De-register, Notify and Send. The Leshan REST shape in `zephyr-interop.md` §3 already covers most of this (Zephyr harness compatibility) but lacks Discover `depth`, Observe parameters, LwM2M CBOR and SenML-ETCH.
2. **Reference client(s)** that can reach C.1/C.3/C.4/C.6/C.12/C.13/C.14 (see §6.2).
3. **Client-side control**: a shell or CLI on the DUT to trigger Send, change values (to cause notifications), stop/start, and corrupt state (int-6, 7, 9).
4. **Raw CoAP injector** for int-105 (spoofed NoSec DELETE) and to check RST behaviour.
5. **PKI and DNS**: our own CA, `server.example.com` and `server-fail.example.com` names, client cert/key (the ETS ZIP is not published).
6. **FOTA assets**: a valid image; oversized, corrupt and wrong-type images; a download server (CoAP block2 or HTTP) with a "drop connection" switch; a bad URI.
7. **Timing assertions**: pmin/pmax windows (304, 308, 309, 313), 20 s lifetime expiry (102), MAX_TRANSMIT_WAIT 93 s (109).
8. **Second server instance** for 950/951, and an MQTT broker for int-19 (only if MQTT is in scope).

### 6.2 Open-source clients and their 1.2 coverage (verified 2026-10-06)

| Client | LwM2M versions | 1.2 features (source) | Transports/security | Notes |
|---|---|---|---|---|
| **Anjay** (AVSystem/Anjay, `master` @ `fdd70854`, 2026-09-18, v3.15.0) | 1.0, 1.1, "most of LwM2M 1.2" (README). CMake `WITH_LWM2M11` ON, `WITH_LWM2M12` ON by default | CHANGELOG 3.13.0 (2026-03-31): **LwM2M CBOR, Bootstrap Pack, Observation Attributes carried in Observe, hqmax and edge, Discover `depth`, SenML-ETCH CBOR & JSON for composite, deleting Resource Instances**. `con` via `WITH_CON_ATTR`. 3.7.0: infinite lifetime (lt=0). `WITH_BOOTSTRAP_PACK`, `WITH_OBSERVATION_ATTRIBUTES` flags in CMakeLists. Client reports "1.2" (`anjay_utils_core.c`). **No Profile ID code** (no `pid=` in `src/`). **No MQTT or HTTP transport** in the tree | UDP/DTLS (mbedTLS, OpenSSL) PSK and cert, NoSec; CoAP+TCP (`WITH_AVS_COAP_TCP`); DTLS CID (`use_connection_id`); Send, all composites, Gateway | **Commercial only**: OSCORE, EST, SMS, HSM, core persistence, bootstrapper/SIM bootstrap (AVSystem CommercialFeatures page). Since 3.10.0 the license is "Non-Commercial License" (commercial use needs free registration) |
| **Leshan client** (eclipse-leshan/leshan `master`) | 1.0, 1.1 only (`LwM2mVersion` defines V1_0, V1_1; `lastSupported()` = V1_1). README: v2.x targets 1.1.x | none | Californium CoAP/CoAPS and **OSCORE** modules for client, server and bsserver (`leshan-tl-cf-*-coap-oscore`); java-coap CoAP and **CoAP-TCP** (`leshan-tl-jc-client-coaptcp`; TLS over TCP not verified) | Best for 1.1 cases incl. 405 (OSCORE) and int-11-style OSCORE bootstrap; cannot report `lwm2m=1.2` |
| **Wakaama** (eclipse-wakaama/wakaama) | 1.0, 1.1 (`lwm2m_version_t` = VERSION_1_0, VERSION_1_1; `WAKAAMA_CLIENT_LWM2M_V_1_0` flag) | none | UDP, DTLS PSK via tinydtls; TLV, JSON, SenML JSON/CBOR; client-initiated bootstrap; BS-server example | **Repository archived** (GitHub `archived: true`, last push 2026-05-26). The only tagged release 1.0 is flagged vulnerable in its README |
| **Zephyr LwM2M** | 1.0 (default) and 1.1 | none (standards.md §4: no LwM2M CBOR, no `lwm2m=1.2`) | UDP, DTLS PSK (+CID), queue mode | Already drives 60 ETS IDs through the interop suite (zephyr-interop.md) |
| Anjay Lite (AVSystem/Anjay-lite) | not verified (README points to an external feature list) | not verified | DTLS via mbedTLS 3.x | Not assessed |

### 6.3 Coverage plan by client

- **Zephyr + its interop suite**: the 60 IDs in §4, as 1.1. This is the regression baseline.
- **Anjay (open source)**: only OSS client for the 1.2 cases int-12 (Bootstrap-Pack), 264, 266 (depth), 312, 313 (Observe attributes). It also covers the 1.2 wire formats (LwM2M CBOR 11544, SenML-ETCH 320/322) in 229/230/235/236/257/280/281/304-311 run as a 1.2 client. It is also usable for 106/404/406 (CoAP+TCP), 402/403 (DTLS cert), 0-9 and the FOTA cases.
- **Leshan client**: 405 (OSCORE), a second 1.1 implementation for cross-checks, and the two-client part of 950/951 together with our second server.
- **No OSS client found for**: int-110/111 (Profile ID), int-19 (MQTT), int-10 (EST; Anjay commercial), int-11 and 405 run as 1.2 with OSCORE (Anjay commercial; Leshan is 1.1 only), int-3 (smartcard). For 110/111 we need our own scripted CoAP client (a raw `POST /rd?lwm2m=1.2&pid=6:27354b9a`) to exercise the server side.
- **Object-specific cases** (701/710 `/4`, 801/810 `/6`, 901-910 `/7`, 1200-1350 `/10-/13`, 1900/1901 `/19`, 2000/2001 `/20`): need a client that implements those objects with C.x values. Anjay and Leshan demo clients expose custom objects; values must be set to match the configuration.

### 6.4 Known ETS defects to handle in the harness

These are interpret-not-copy points (all marked **[ETS erratum]** above):
- Lifetime and Mute Send written with "CoAP POST" on a resource (105, 109, 307); 228's resource-instance "POST". Use PUT.
- int-310: "Observe Option set to 1"; 304: "Observe = 1" in notifications. Use Observe=0 for registration and accept any sequence number.
- int-12 pass criterion expects 2.04 for a Pack-Request; it gets 2.05.
- int-11/19 Discover expectations name `/1/0` and `lwm2m="1.1"` inconsistently with what was written.
- int-405 references C.19; use C.22.
- C.14 ACL entries all target `/2/0`; use `/2/0`, `/2/1`, `/2/2`.
- int-774 expects Result 2 for out-of-RAM; Object 5 says 3.
- int-1202 TLV `C10 0D 02` is malformed; correct is `C1 0E 02`.
- int-1900 expects `/19` ver 1.1.
- int-220 JSON `0101`, int-0 `"bs":"U"`, C.3 `"vd":false`, int-261 `&` separators: parse leniently or fix in fixtures.
- Delete success is written as "2.04 Deleted" in 309/1635; CoAP is 2.02.
