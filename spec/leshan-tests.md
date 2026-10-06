# Leshan test mining: server edge-case checklist

Source: Eclipse Leshan @ `22bc753133829b91aa1f090e1e1179e6957940a0`. These tests are mined as a checklist for our own black-box tests of the Go LwM2M server. We do NOT port them.
Scope: `leshan-integration-tests`, plus the server-side unit tests in `leshan-lwm2m-server`, `leshan-lwm2m-servers-shared`, `leshan-lwm2m-server-redis`, `leshan-lwm2m-bsserver`, `leshan-tl-cf-server-coap`, `leshan-tl-cf-bsserver-coap` and `leshan-tl-cf-shared-oscore`. `leshan-tl-jc-server-coap`, `leshan-tl-jc-server-coaptcp`, `leshan-tl-cf-server-coap-oscore` and `leshan-tl-cf-bsserver-coap-oscore` have no tests.
Columns: test (Class#method, file:line) | behavior asserted | server-relevant? (yes/no/client-only) | edge-case notes. Counts are test methods, not parameter/transport expansions.

## Codec/node tests (out of scope, names only; covered by another agent)
`leshan-lwm2m-core/src/test/java/org/eclipse/leshan/`:
core/json/{JsonArrayEntryTest, JsonDeserializerTest, JsonRootObjectTest, JsonSerializerTest};
core/link/{DefaultLinkParserTest, DefaultLinkSerializerTest, LinkParserValidationTest, LinkTest, MixedLwM2mLinkTest};
core/link/attributes/{AttributeSetTest, AttributeTest, BaseAttributeTest, ValuelessAttributeTest};
core/link/lwm2m/{DefaultLwM2mLinkParserTest}; core/link/lwm2m/attributes/{DefaultLwM2mAttributeParserTest, LwM2mAttributesTest, LwM2mAttributeTest};
core/node/{LwM2mMultipleResourceTest, LwM2mNodeUtilTest, LwM2mObjectInstanceTest, LwM2mObjectTest, LwM2mPathTest, LwM2mResourceInstanceTest, LwM2MResourceTest, LwM2mRootTest, LwM2mSingleResourceTest, ObjectLinkTest, PrefixedLwM2mPathTest, TimestampedLwM2mNodesTest, TimestampedLwM2mNodeTest};
core/node/codec/{LwM2mNodeDecoderEncoderTest, LwM2mNodeDecoderTest, LwM2mNodeEncoderTest};
core/request/{AbstractSimpleDownlinkRequestTest, WriteAttributesRequestTest};
core/senml/cbor/{AbstractSenMLTest, SenMLCborSerializerTest, SenMLJsonSerDesTest}; senml/{SenMLPackTest, SenMLRecordTest};
core/tlv/{TlvDecoderTest, TlvEncoderTest, TlvTest}; core/util/base64/{DefaultBase64DecoderTest, DefaultBase64EncoderTest};
client: `leshan-lwm2m-client/.../client/util/LinkFormatHelperTest`. Server: `RegistrationSortObjectLinksTest` (listed under Registration).

## Conventions (per source group)

**Group A (Registration/queue/redis/lockstep)**

Path prefixes: `IT/` = `leshan-integration-tests/src/test/java/org/eclipse/leshan/integration/tests/`; `SRV/` = `leshan-lwm2m-server/src/{test,main}/java/org/eclipse/leshan/server/`; `RED/` = `leshan-lwm2m-server-redis/src/{test,main}/java/org/eclipse/leshan/server/redis/`; `SHR/` = `leshan-lwm2m-servers-shared/src/{test,main}/java/org/eclipse/leshan/servers/`.

Transport matrices:
- `IT/RegistrationTest`, `IT/QueueModeTest`: COAP {Cf client/Cf server, Cf/java-coap, java-coap/Cf, java-coap/java-coap} + COAP_TCP {java-coap/java-coap} (`IT/RegistrationTest.java:84-91`, `IT/QueueModeTest.java` same block).
- `IT/MultiEndpointsTest`: the 4 COAP combos only, no TCP. Server has 2 endpoints of the same provider; the client sits behind a `ReverseProxy` that can switch its target server endpoint (`IT/MultiEndpointsTest.java:83-102`).
- `IT/DeleteClientOnlyTest`: COAP, Californium server, client Cf or java-coap.
- `IT/lockstep/LockStepTest`: COAP, server Cf or java-coap. The raw lockstep client sends hand-crafted CoAP. Retransmission is tuned so that an un-ACKed CON request times out in about 1s (`IT/lockstep/LockStepTest.java:124-156`).
- Test defaults: client lifetime 300s (`IT/util/LeshanTestClientBuilder.java:105`), client request timeout 800ms (`:207`), server `waitFor*` waits 1s, DTLS retransmission 300ms (`IT/util/LeshanTestServerBuilder.java:252`), Redis prefix `LESHAN_TEST_REGSTORE#` (`IT/util/RedisTestUtil.java:36-40`).

**Group B (Device-management ops)**

Transport matrix ("T5") used by every class below unless noted: CoAP {Cf client/Cf server, Cf/java-coap, java-coap/Cf, java-coap/java-coap} + CoAP-TCP {java-coap/java-coap}. `hasValidUnderlyingResponseFor` only checks the raw CoAP response object type (AbstractLwM2mResponseAssert.java:61); `hasContentFormat` checks the Content-Format option of the response equals the requested one (AbstractLwM2mResponseAssert.java:78). Test client exposes `</1/0>,</2>,</3/0>,</3442/0>` (ACL /2 supported but empty; /4 absent).

**Group C (Observe/send/cf-coap unit)**

Path prefixes: `IT/` = `leshan-integration-tests/src/test/java/org/eclipse/leshan/integration/tests/`; `CF/` = `leshan-tl-cf-server-coap/src/test/java/org/eclipse/leshan/transport/californium/server/`; `CFm/` = `leshan-tl-cf-server-coap/src/main/java/org/eclipse/leshan/transport/californium/server/`; `JCm/` = `leshan-tl-jc-server-coap/src/main/java/org/eclipse/leshan/transport/javacoap/server/`; `SRVm/` = `leshan-lwm2m-server/src/main/java/org/eclipse/leshan/server/`.
`leshan-tl-cf-server-coap-oscore`, `leshan-tl-jc-server-coap`, `leshan-tl-jc-server-coaptcp` have **no tests** (only `logback-leshan-test.xml`).

Transport matrix "T5" = {COAP: cf->cf, cf->jc, jc->cf, jc->jc; COAP_TCP: jc->jc} (client impl -> server impl). "S2" = server cf|jc, client Californium, COAP only.

**Group D (Bootstrap/security)**

Path prefixes: `IT/` = `leshan-integration-tests/src/test/java/org/eclipse/leshan/integration/tests/`; `BS/` = `leshan-lwm2m-bsserver/src/test/java/org/eclipse/leshan/bsserver/`; `CFBS/` = `leshan-tl-cf-bsserver-coap/src/test/java/org/eclipse/leshan/transport/californium/bsserver/`.
Transport matrix: BootstrapTest runs COAP x {cf/cf, cf/jc, jc/cf, jc/jc} client/server, BS server always Californium (`IT/bootstrap/BootstrapTest.java:92-95`). PSK/RPK/RpkX509/SNI: COAPS Californium only. X509Test: COAPS (cf) + COAPS_TCP (java-coap) (`IT/security/X509Test.java:81-82`). OSCORE: COAP "Californium-OSCORE" only.
Default test BS config: BS-server Security at `/0/0` (`bootstrapServer=true`), DM Security `/0/1` + Server `/1/0` with ssid=2222 (`IT/util/BootstrapConfigTestBuilder.java:56,126-132`).

## Tests by area

### Registration

| test (Class#method, file:line) | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| RegistrationTest#register_update_deregister `IT/RegistrationTest.java:119` | Register → 2.01 and `registered` event. Object links stored verbatim (`</>;rt="oma.lwm2m";ct="60 110 112 1542 1543 11542 11543",</1/0>,</2>,</3/0>,</3442/0>`). With lt=2s the client sends Update → 2.04 and `updated(prevReg)`. Deregister → 2.02 and `unregistered(expired=false, obs=[])` | yes | Root link `</>` carries rt and ct. ct is a quoted space-separated list |
| RegistrationTest#register_with_additional_attributes `:271` | Unknown query params on Register (`key1=value1`, `imei=...`) are stored as additional attributes, exact map | yes | Server must keep params it does not recognise and not reject them |
| RegistrationTest#register_without_sending_endpoint `:296` | Register without `ep=` on unsecured CoAP → 4.03 FORBIDDEN, no registration | yes | Endpoint is derived from the security identity (PSK id / X509 CN / OSCORE recipient id), else FORBIDDEN (`SRV/registration/RegistrationHandler.java:66-73`, `SHR/DefaultServerEndpointNameProvider.java:49-65`) |
| RegistrationTest#register_update_reregister `:239` | Client stops without deregistering, restarts, registers again with the same ep → server fires `unregistered(oldReg, obs=[], expired=false, newReg)` then `registered(newReg, previousReg=old)` (`IT/util/LeshanTestServer.java:217-231`) | yes | Re-register replaces the registration by endpoint name. The event pair is ordered unregister then register |
| RegistrationTest#register_update_deregister_reregister `:205` | Full deregister, then a fresh Register → new `registered` event (no previousReg) | yes | Same ep can register again after a Deregister |
| LockStepTest#register_with_invalid_request `IT/lockstep/LockStepTest.java:180` | POST /rd?ep=X, ct=link-format, empty payload → 4.00 BAD_REQUEST | yes | Object links are mandatory (`leshan-lwm2m-core/.../request/RegisterRequest.java:86-89`) |
| LockStepTest#register_with_uq_binding_in_lw_1_0 `:199` | Register lwm2m=1.0, b=UQ → 2.01 | yes | Q is legal in 1.0 |
| LockStepTest#register_with_ut_binding_in_lw_1_1 `:210` | Register lwm2m=1.1, b=UT → 2.01 | yes | T is legal in 1.1 |
| LockStepTest#register_update_with_invalid_binding_for_lw_1_1 `:221` | Register 1.1 b=U → 2.01. Update b=U → 2.04. Register 1.1 b=UQ → 4.00. Second "invalid update" → 4.00 | yes | Q is not allowed in 1.1 or later, T/N not before 1.1 (`core/.../BindingMode.java:47-63`). **Test bug:** the "invalid update" is built from the RegisterRequest (`:247`), so it is really a second Register. The Update path validates against the *registered* version (`SRV/registration/RegistrationHandler.java:154-155`) |
| LockStepTest#register_update_with_invalid_binding_for_lw_1_0 `:255` | Register 1.0 b=U → 2.01. Update → 2.04. Register 1.0 b=UT → 4.00, and again → 4.00 | yes | Same test bug as above |
| registration.RegistrationHandlerTest#test_application_data_from_authorizer `SRV/registration/RegistrationHandlerTest.java:61` | Authorizer custom data is attached on Register and *replaced* by new custom data on Update | yes (internal) | Per-registration app data comes from the auth step |
| RegistrationHandlerTest#test_update_without_application_data_from_authorizer `:93` | An Update whose authorizer returns no custom data keeps the previous custom data | yes (internal) | null means unchanged |
| RegistrationHandlerTest#test_unsupported_lwm2m_version `:122` | Register with lwm2m=1.2 → 4.12 PRECONDITION_FAILED, nothing stored | yes | Unsupported version gives 4.12, not 4.00 (`SRV/registration/RegistrationHandler.java:78-82`, `DefaultRegistrationDataExtractor.java:42`) |
| registration.RegistrationTest#test_object_links_without_version_nor_rootpath `SRV/registration/RegistrationTest.java:50` | `</1/0>,</3/0>` → rootPath "/", supported {1,3} at default version, instances {/1/0,/3/0} | yes | No root link |
| …#test_object_links_with_ct_but_with_rt `:69` | `</>;ct="0 42 11543"` → CFs {TEXT,OPAQUE,JSON} + TLV implicitly (4) | yes | Test name says "with_rt" but there is no rt. A root link with only ct still counts. TLV always added for 1.0 |
| …#test_object_links_with_ct_with_1_content_format_with_quote `:94` | `ct="42"` → {OPAQUE, TLV} | yes | |
| …#test_object_links_with_ct_with_1_content_format_without_quote `:118` | `ct=42` (unquoted) → {OPAQUE, TLV} | yes | Accept both quoted and unquoted ct |
| …#test_object_links_with_default_rootpath `:142` | `</>;rt="oma.lwm2m";ct="0 42 11543"` → rootPath "/", 4 CFs | yes | |
| …#test_object_links_with_rootpath `:168` | `</root>;rt="oma.lwm2m",</root/1/0>,</3/0>` → rootPath "/root/". Only /1 supported, `</3/0>` is ignored | yes | With an alternate path, links outside it are dropped |
| …#test_object_links_with_unquoted_rootpath `:187` | `rt=oma.lwm2m` unquoted works the same | yes | |
| …#test_object_links_with_version `:206` | `</3>;ver=1.1` → object 3 at version 1.1, others default | yes | `ver` attribute on the object link |
| …#test_object_links_with_text_in_not_lwm2m_path `:225` | Non-numeric segments outside the root path are ignored. `</root/4/0/0/>` (a resource-level link) does not add object 4 | yes | Only object and instance level links count |
| …#test_object_links_with_text_in_lwm2m_path `:244` | Malformed link-format (`<text>` without a leading slash, bare `empty`) → LinkParseException | yes | Parse failure becomes 4.00 at the transport layer |
| …#test_object_links_with_version_for_lwm2m_v1_1 `:252` | lwm2m=1.1 with no `ver` → default object versions follow the 1.1 core registry: 1→1.1, 2→1.0, 3→1.1, 4→1.2, 5..7→1.0 | yes | Default object version depends on the LwM2M version |
| …#assertEqualsHashcode `:300` | Registration equals/hashCode contract | no | Java only |
| RegistrationSortObjectLinksTest#sort_link_object_on_get `SRV/registration/RegistrationSortObjectLinksTest.java:35` | Sorted object links: null first, then by URI (`/0/2` < `/0/1024/2`) | no (UI nicety) | Numeric segment ordering |

### Update

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| InMemoryRegistrationStoreTest#update_registration_keeps_properties_unchanged `SRV/registration/InMemoryRegistrationStoreTest.java:58` | Update with every field null keeps lifetime, binding and sms. Returns previous and updated registration | yes | Update only touches the fields present in the request |
| InMemoryRegistrationStoreTest#update_registration_to_extend_time_to_live `:86` | A registration with lt=0 is not alive. Update with lt=10000 → alive | yes | Update refreshes lastUpdate, so expiry = lastUpdate + lifetime |
| RegistrationUpdateTest#testAdditionalAttributesUpdate `SRV/registration/RegistrationUpdateTest.java:40` | Update additional attributes are *merged*: existing keys overwritten, new keys added | yes | Merge, not replace |
| RegistrationUpdateTest#testApplicationDataUpdate `:74` | Update with null custom data keeps the old custom data | yes (internal) | |
| RegistrationUpdateTest#assertEqualsHashcode `:99` | equals contract | no | |

### Deregister

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| RegistrationTest#deregister_cancel_multiple_pending_request `IT/RegistrationTest.java:149` | Client goes silent and 4 Reads to it are in flight (retransmitting). The client then registers again → all 4 pending requests fail with RequestCanceledException and no response callback ever runs | yes | Pending downlink requests are cancelled on unregistered/re-register (`SRV/LeshanServer.java:240-243`). Skipped on TCP |
| LockStepTest#register_deregister_observe `IT/lockstep/LockStepTest.java:406` | Register, Deregister, then server sends Observe on the stale registration → Cf: SendFailedException before sending. java-coap: request goes out, and a 2.05+Observe reply is silently dropped. Afterwards: no registrations, no observations stored | yes | Never persist an observation for a dead registration. Fail early. Also covered in part B (observe) |

### Queue mode

Server awake time is 1000ms (`IT/QueueModeTest.java` setup `withAwakeTime`). The production default is 93s (`SRV/queue/StaticClientAwakeTimeProvider.java:29`).

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| QueueModeTest#awake_sleeping_awake_sleeping `IT/QueueModeTest.java:97` | Register with Q → `onAwake`. Still awake at 0.8×awakeTime, then `onSleeping`. Update → awake again, then sleeping again | yes | The awake timer starts at Register or Update |
| QueueModeTest#one_awake_notification `:140` | Several Updates while already awake give only one `onAwake`. No duplicate awake/sleeping events within awakeTime/2 | yes | Presence events fire only on state change. Each Update restarts the timer (`SRV/queue/PresenceServiceImpl.java:74-115`) |
| QueueModeTest#sleeping_if_timeout `:185` | Client stops. A Read with 1ms timeout → null response and the client is marked sleeping at once | yes | A request timeout marks the client sleeping (`SRV/queue/QueueModeLwM2mRequestSender.java:73-80,121-131`). COAP only. TCP should give UnconnectedPeer (TODO) |
| QueueModeTest#correct_sending_when_awake `:221` | Read while awake → response. After sleep and an Update wake-up, Read → response | yes | Requests while sleeping throw ClientSleepingException and are not queued (`QueueModeLwM2mRequestSender.java:66-68`) |
| PresenceServiceTest#testSetOnlineForNonQueueMode `SRV/queue/PresenceServiceTest.java:43` | `setAwake` on a non-Q registration fires no events | yes | Presence tracking applies only to Q clients |
| PresenceServiceTest#testIsOnline `:61` | Q registration is awake after setAwake. After setSleeping it is not | yes | |

### Read

Formats: ReadSingleValueTest = TEXT, TLV, CBOR, JSON, SENML_JSON, SENML_CBOR x T5 (ReadSingleValueTest.java:71). ReadMultiValueTest = TLV, JSON, SENML_JSON, SENML_CBOR x T5 (ReadMultiValueTest.java:70). ReadOpaqueValueTest = OPAQUE + the 6 above x T5 (ReadOpaqueValueTest.java:71). ReadFailedTest = T5 only, no Accept.

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| ReadSingleValueTest#can_read_resource (read/ReadSingleValueTest.java:117) | GET /3/0/1 with Accept=fmt -> 2.05, response Content-Format == fmt, value "IT-TEST-123" | yes | server must decode every single-value format |
| ReadSingleValueTest#can_read_resource_instance (:133) | GET /3442/0/1110/0 Accept=fmt -> 2.05, LwM2mResourceInstance id 0 | yes | resource-instance path decoding for TEXT/CBOR too |
| ReadSingleValueTest#cannot_read_non_multiple_resource_instance (:150) | GET /3442/0/<single int res>/0 -> 4.00 with diagnostic payload "invalid path : resource is not multiple" | yes | error comes from client (leshan-lwm2m-client/.../BaseObjectEnabler.java:80); server must surface error payload text |
| ReadMultiValueTest#can_read_empty_object (read/ReadMultiValueTest.java:115) | GET /2 (no instances) -> 2.05, object id 2, zero instances | yes | empty-payload/empty-object decoding per format |
| ReadMultiValueTest#can_read_object (:131) | GET /3 -> 2.05, object 3 with instance 0 | yes | |
| ReadMultiValueTest#can_read_object_instance (:147) | GET /3/0 -> 2.05, instance 0 | yes | |
| ReadOpaqueValueTest#can_read_empty_opaque_resource (read/ReadOpaqueValueTest.java:118) | POST exec /3442/0/<CLEAR_VALUES> success; then GET opaque res Accept=fmt -> 2.05, type OPAQUE, value = 0 bytes | yes | zero-length opaque in every format, incl. TEXT (base64 "") and OPAQUE (empty payload) |
| ReadFailedTest#cannot_read_non_readable_resource (read/ReadFailedTest.java:97) | GET /3/0/4 (executable) -> 4.05 | yes | |
| ReadFailedTest#cannot_read_non_existent_object (:109) | GET /50 -> 4.04 | yes | server sends request even for object absent from registration (no local pre-check) |
| ReadFailedTest#cannot_read_non_existent_instance (:121) | GET /3/1 -> 4.04 | yes | |
| ReadFailedTest#cannot_read_non_existent_resource (:133) | GET /3/0/50 -> 4.04 | yes | |
| ReadFailedTest#cannot_read_security_resource (:145) | GET /0/0/0 from DM server -> 4.04 | yes | Security object hidden from DM servers (4.04, not 4.01/4.05) |

### Write

Formats: WriteSingleValueTest/WriteOpaqueValueTest = TEXT, CBOR, TLV, TLV-old(1542), JSON-old(1543), JSON, SENML_JSON, SENML_CBOR (+OPAQUE for Opaque) x T5 (WriteSingleValueTest.java:87, WriteOpaqueValueTest.java:75). WriteMultiValueTest = TLV, TLV-old, JSON-old, JSON, SENML_JSON, SENML_CBOR x T5 (WriteMultiValueTest.java:82). WriteFailedTest = T5, default fmt TLV (leshan-lwm2m-core/.../request/WriteRequest.java:485). Replace=PUT, Update=POST (leshan-tl-cf-server-coap/.../request/CoapRequestBuilder.java:114).

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| WriteSingleValueTest#write_string_resource (write/WriteSingleValueTest.java:135) | PUT /3442/0/STRING fmt -> 2.04; read-back equals | yes | encoding of all formats incl. legacy 1542/1543 codes |
| WriteSingleValueTest#write_boolean_resource (:155) | PUT bool -> 2.04; read-back | yes | |
| WriteSingleValueTest#write_integer_resource (:175) | PUT -999 -> 2.04; read-back | yes | negative ints |
| WriteSingleValueTest#can_write_string_resource_instance (:195) | PUT /3442/0/1110/0 -> 2.04; read-back resource instance | yes | resource-instance target |
| WriteSingleValueTest#write_float_resource (:220) | PUT 999.99 -> 2.04; read-back | yes | |
| WriteSingleValueTest#write_time_resource (:240) | PUT Date(946681000 ms) -> 2.04; read-back equal | yes | time has second accuracy; ms truncated |
| WriteSingleValueTest#write_corelnk_resource (:260) | PUT corelnk `</3>;ver=1.2,</3/1>,</3/1/0>;attr1="attr1Value";attr2=attr2Value` -> 2.04; read-back equal | yes | quoted vs unquoted attribute values must round-trip |
| WriteSingleValueTest#write_unsigned_integer_resource (:286) | PUT 18446744073709551615 -> 2.04; read-back (default fmt) equal | yes | u64 max, not fitting signed long |
| WriteSingleValueTest#write_objlnk_resource (:307) | PUT objlnk 10245:1 -> 2.04 | yes | |
| WriteSingleValueTest#can_write_single_instance_objlnk_resource (:327) | PUT /3442/0/<MULTI_OBJLNK>/0 objlnk -> 2.04 | yes | |
| WriteSingleValueTest#send_writerequest_synchronously_with_bad_payload_raises_codeexception (:349) | Write string into /3/0/13 (Time) -> CodecException thrown locally, nothing sent | yes | server encodes with registration's model and fails before send |
| WriteSingleValueTest#send_writerequest_asynchronously_with_bad_payload_raises_codeexception (:360) | same, async API -> exception thrown synchronously, not via error callback | yes | encode errors are synchronous even for async send |
| WriteMultiValueTest#can_write_object_instance (write/WriteMultiValueTest.java:128) | PUT /3/0 {14,15} -> 2.04; read /3/0 contains them | yes | |
| WriteMultiValueTest#can_write_replacing_object_instance (:151) | write /1/0/3=60; PUT /1/0 {1,2,6,7} -> 2.04; read: /1/0/3 absent | yes | Replace on instance removes omitted optional writable resources |
| WriteMultiValueTest#can_write_updating_object_instance (:188) | write /1/0/3; POST /1/0 {1,2} -> 2.04; 3,6,7 still present | yes | Update keeps others |
| WriteMultiValueTest#can_write_multi_instance_objlnk_resource (:221) | PUT multi objlnk {0,1,2} -> 2.04; read-back | yes | |
| WriteMultiValueTest#can_write_object_instance_with_empty_multi_resource (:252) | PUT /3442/0 {1110:{0,1},1120:{0,1}} -> 2.04; then PUT /3442/0 {1110:{3}} -> 2.04; 1120 removed, 1110 == {3} | yes | Replace instance drops multi-res not included |
| WriteMultiValueTest#can_write_object_resource_instance (:319) | PUT multi {10,20}; POST update {20,30} -> merge {10,20',30}; PUT {1} -> exactly {1}; reads 2.05 with fmt | yes | resource-level Update merges instances, Replace replaces all |
| WriteOpaqueValueTest#write_opaque_resource (write/WriteOpaqueValueTest.java:124) | PUT opaque {1,2,3} fmt -> 2.04; read-back | yes | OPAQUE ct=42 raw payload |
| WriteOpaqueValueTest#write_opaque_resource_instance (:145) | PUT TLV multi opaque {2,3}; read each instance fmt; PUT instance 3 fmt -> 2.04; read-back | yes | sparse instance ids |
| WriteFailedTest#cannot_write_non_writable_resource (write/WriteFailedTest.java:101) | PUT /3/0/0 -> 4.05 | yes | |
| WriteFailedTest#cannot_write_security_resource (:114) | PUT /0/0/0 -> 4.04 | yes | security hidden |
| WriteFailedTest#cannot_write_replacing_incomplete_object_instance (:127) | PUT /1/0 {1,2} only -> 4.00 | yes | client rejects Replace missing mandatory resources (6,7) |

### Write-attributes

WriteAttributeObserveTest/HouseKeeping/Discover/Failed = T5. WriteAttributeBootstrapTest = CoAP only (4 combos), BS server always Californium (WriteAttributeBootstrapTest.java:72). Write-Attributes = PUT with Uri-Query per attribute, no payload (CoapRequestBuilder.java:124).

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| WriteAttributeFailedTest#test_failing (attributes/WriteAttributeFailedTest.java:142) | 6 cases: initial attrs accepted (2.04) then 2nd PUT making merged set invalid -> 4.00. Cases: pmin300 then pmax200 (/3); epmin300/epmax200; gt100 then lt200; gt200/lt200; gt11 then lt10+st2; gt14 then lt10+st2 (:69-102) | yes | split across 2 requests so server-side single-set validation passes; client validates merged set. Rules: pmin<=pmax, epmin<=epmax, lt<gt, lt+2*st<gt (leshan-lwm2m-core/.../attributes/MixedLwM2mAttributeSet.java:90-133) |
| WriteAttributeDiscoverTest#write_attribute_on_object_then_discover (attributes/WriteAttributeDiscoverTest.java:142) | discover /3 has no attrs; WA /3 pmin=100&pmax=200 -> 2.04; discover shows `</3>;pmin=100;pmax=200`; WA pmin=150 -> override keeps pmax; WA `pmax` (no value) -> removes pmax; WA pmin=300,pmax=600 | yes | valueless query param = unset attribute; WA merges, not replaces |
| WriteAttributeDiscoverTest#write_attribute_on_object_instance_then_discover (:220) | WA /3/0 pmin/pmax -> 2.04; discover /3 and /3/0 show attrs on `</3/0>` | yes | |
| WriteAttributeDiscoverTest#write_attribute_on_single_resource_then_discover (:257) | WA /3/0/9 pmin,pmax,st=1,lt=20,gt=50 -> 2.04; discover at /3, /3/0, /3/0/9 shows `</3/0/9>;pmin=100;pmax=200;st=1;lt=20;gt=50` | yes | attribute order in link output: pmin;pmax;st;lt;gt |
| WriteAttributeDiscoverTest#write_attribute_on_resource_instance_then_discover (:305) | WA /3/0/7/0 -> 2.04; NOT shown at /3 or /3/0 discover; shown at /3/0/7 discover on `</3/0/7/0>` | yes | resource-instance attrs only visible at resource-level discover |
| WriteAttributeDiscoverTest#write_attribute_at_all_level_then_discover (:351) | WA at /3, /3/0, /3/0/7, /3/0/7/0 -> 2.04 each; discover per level shows each level's own attrs (no inheritance merge in output); `dim=2` precedes attrs | yes | |
| WriteAttributeObserveTest#test_pmin (attributes/WriteAttributeObserveTest.java:124) | WA pmin=1 on int res; observe 2.05; write 50 -> notification arrives ~1s (±0.2) later with 50 | yes | server must tolerate delayed notif |
| WriteAttributeObserveTest#test_pmax (:156) | WA pmax=1; observe; no change -> notification at ~1s with current value 1024 | yes | |
| WriteAttributeObserveTest#test_pmin_and_pmax (:185) | pmin=1,pmax=2: write 30 -> notif at ~1s; next notif ~2s later | yes | |
| WriteAttributeObserveTest#test_pmin_equals_pmax (:193) | pmin=pmax=1 accepted, same timing | yes | pmin==pmax is valid |
| WriteAttributeObserveTest#test_lt_integer_resource (:234) | lt=500; 800 -> no notif (200ms); 450 -> notif within 100ms; then none | yes | uses PUT TLV writes |
| WriteAttributeObserveTest#test_lt_float_resource (:249) | lt=2.5; 4.3 none; 2.1 notif | yes | |
| WriteAttributeObserveTest#test_lt_unsigned_integer_resource (:264) | lt=500 on uint; 800 none; 450 notif | yes | |
| WriteAttributeObserveTest#test_gt_integer_resource (:279) | gt=2000; 1500 none; 3000 notif | yes | |
| WriteAttributeObserveTest#test_gt_float_resource (:294) | gt=5.5; 4.3 none; 5.6 notif | yes | |
| WriteAttributeObserveTest#test_gt_unsigned_integer_resource (:309) | gt=1e19; 9999999999999990000 none; 10000000000000010000 notif | yes | gt query param as large decimal |
| WriteAttributeObserveTest#test_st_with_positive_gap_on_integer_resource (:326) | st=200 from 1024: 1124 none; 1225 notif | yes | step measured from last notified value |
| WriteAttributeObserveTest#test_st_with_positive_gap_on_float_resource (:341) | st=200: 103.14 none; 204.14 notif | yes | |
| WriteAttributeObserveTest#test_st_with_positive_gap_on_unsigned_integer_resource (:356) | st=20 near 2^63: +10 none; +20 notif | yes | u64 > i64 arithmetic |
| WriteAttributeObserveTest#test_st_with_negative_gap_on_integer_resource (:373) | st=200: 924 none; 823 notif | yes | abs difference |
| WriteAttributeObserveTest#test_st_with_negative_gap_on_float_resource (:388) | st=200: -103 none; -203 notif | yes | |
| WriteAttributeObserveTest#test_st_with_negative_gap_on_unsigned_integer_resource (:403) | st=20: -8 none; -50 notif | yes | |
| WriteAttributeObserveTest#test_lt_pmax_attributes (:457) | lt=500,pmax=1: 1200 none; 400 notif; then notif ~1s later | yes | |
| WriteAttributeObserveTest#test_lt_gt_st_attributes_on_integer_resource (:500) | init 1024; lt=1000,gt=1201,st=100: 1050 none; 990 notif (lt cross); 1100 notif (st); 1210 notif (gt) | yes | lt/gt/st OR-combined |
| WriteAttributeObserveTest#test_lt_gt_st_attributes_on_float_resource (:524) | same on float | yes | |
| WriteAttributeObserveTest#test_lt_gt_st_attributes_on_unsigned_integer_resource (:548) | same on uint | yes | |
| WriteAttributeObserveTest#test_object_inheritance (:629) | WA pmax=1 on /3442; observe /3442/0/1120/0 -> notif ~1s | yes | attrs inherit down to resource instance |
| WriteAttributeObserveTest#test_object_instance__inheritance (:640) | WA pmax on /3442/0; observe res instance -> ~1s notif | yes | |
| WriteAttributeObserveTest#test_resource__inheritance (:651) | WA pmax on resource; observe res instance -> ~1s notif | yes | |
| WriteAttributeObserveTest#test_invalid_inheritance_raise_exception (:688) | WA pmin=200 on /3442/0/1120 (2.04), WA pmax=100 on /3442/0/1120/0 (2.04); Observe instance -> 5.00; no observation state on client | yes | each WA valid alone; effective (inherited) set invalid only detected at Observe; server must handle 5.00 observe (no observation created) |
| WriteAttributeHouseKeepingTest#write_attribute_on_tree_then_remove_object_instance (attributes/WriteAttributeHouseKeepingTest.java:122) | WA pmin=10 on /3442/0, /3442/0/1120, /3442/0/1120/0 (success); DELETE /3442/0 success; client holds no attrs for those paths | client-only | attrs garbage-collected with node |
| WriteAttributeHouseKeepingTest#write_attribute_on_tree_then_remove_resource_instance (:144) | WA on /3442/0/1120/0; PUT TLV /3442/0/1120 {1:10} (replace removes instance 0); attrs for /…/0 gone | client-only | |
| WriteAttributeHouseKeepingTest#write_attribute_then_observe_object_then_passive_cancel_then_check_no_more_notification_data (:196) | WA pmax=1 on /3442; observe; server cancels locally (no CoAP); next notif (≤1.2s) answered by RST -> client drops notification state | yes | server must RST notifications of unknown/cancelled observations; skipped on TCP (no passive cancel) |
| WriteAttributeHouseKeepingTest#…object_instance…passive_cancel… (:206) | same on /3442/0 | yes | UDP only |
| WriteAttributeHouseKeepingTest#…resource…passive_cancel… (:216) | same on /3442/0/1120 | yes | UDP only |
| WriteAttributeHouseKeepingTest#…resource_instance…passive_cancel… (:226) | same on /3442/0/1120/0 | yes | UDP only |
| WriteAttributeHouseKeepingTest#write_attribute_then_observe_object_then_active_cancel_then_check_no_more_notification_data (:264) | WA pmax=1; observe; CancelObservationRequest (GET Observe=1) -> success; client has no notification data | yes | active cancel works on TCP too |
| WriteAttributeHouseKeepingTest#…object_instance…active_cancel… (:273) | same /3442/0 | yes | |
| WriteAttributeHouseKeepingTest#…resource…active_cancel… (:282) | same resource | yes | |
| WriteAttributeHouseKeepingTest#…resource_instance…active_cancel… (:291) | same resource instance | yes | |
| WriteAttributeHouseKeepingTest#write_attribute_then_observe_object_then_remove_object_then_check_no_more_notification_data (:329) | WA pmin=2; observe; client removes object enabler -> no notification data | client-only | |
| WriteAttributeHouseKeepingTest#…object_instance…remove_object… (:338) | same | client-only | |
| WriteAttributeHouseKeepingTest#…resource…remove_object… (:347) | same | client-only | |
| WriteAttributeHouseKeepingTest#…resource_instance…remove_object… (:356) | same | client-only | |
| WriteAttributeBootstrapTest#write_attribute_then_observe_object_then_bootstrap_then_check_no_more_notification_data (attributes/WriteAttributeBootstrapTest.java:110) | client bootstraps (BS + DM config), registers; WA pmin=1 /3442; observe; client-initiated re-bootstrap -> client notification state cleared | client-only (server: expect observations gone after re-bootstrap/re-register) | |
| WriteAttributeBootstrapTest#…object_instance…bootstrap… (:120) | same /3442/0 | client-only | |
| WriteAttributeBootstrapTest#…resource…bootstrap… (:130) | same /3442/0/1120 | client-only | |
| WriteAttributeBootstrapTest#…resource_instance…bootstrap… (:140) | same /3442/0/1120/0 | client-only | |

### Discover

DiscoverTest = T5; GET with Accept=40 link-format (CoapRequestBuilder.java:108). Custom device: res 11 multi with instances {1,3} (DiscoverTest.java:75).

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| DiscoverTest#can_discover_object (DiscoverTest.java:129) | GET /3 ct=40 -> 2.05 `</3>,</3/0>,</3/0/0>,</3/0/1>,</3/0/2>,</3/0/11>;dim=2,</3/0/14>,</3/0/15>,</3/0/16>` | yes | object-level discover lists object, instance, resources; multi-res has `dim`; no resource instances listed |
| DiscoverTest#cant_discover_non_existent_object (:144) | GET /4 -> 4.04 | yes | |
| DiscoverTest#can_discover_object_instance (:156) | GET /3/0 -> 2.05, starts at `</3/0>` | yes | |
| DiscoverTest#cant_discover_non_existent_instance (:169) | GET /3/1 -> 4.04 | yes | |
| DiscoverTest#can_discover_single_resource (:181) | GET /3/0/0 -> `</3/0/0>` | yes | |
| DiscoverTest#can_discover_multi_instance_resource (:194) | GET /3/0/11 -> `</3/0/11>;dim=2,</3/0/11/1>,</3/0/11/3>` | yes | resource discover lists resource instances (sparse ids) |
| DiscoverTest#cant_discover_resource_of_non_existent_object (:207) | GET /4/0/0 -> 4.04 | yes | |
| DiscoverTest#cant_discover_resource_of_non_existent_instance (:219) | GET /3/1/0 -> 4.04 | yes | |
| DiscoverTest#cant_discover_resource_of_non_existent_instance_and_resource (:231) | GET /3/1/20 -> 4.04 | yes | |
| DiscoverTest#cant_discover_resource_of_non_existent_resource (:243) | GET /3/0/42 -> 4.04 | yes | |

### Execute

ExecuteTest = T5. POST, args as text/plain payload when present (CoapRequestBuilder.java:135).

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| ExecuteTest#cannot_execute_read_only_resource (ExecuteTest.java:102) | POST /3/0/0 -> 4.05 | yes | |
| ExecuteTest#cannot_execute_read_write_resource (:114) | POST /3/0/13 -> 4.05 | yes | |
| ExecuteTest#cannot_execute_nonexisting_resource_on_existing_object (:126) | POST /3/0/9999 -> 4.04 | yes | |
| ExecuteTest#cannot_execute_nonexisting_resource_on_non_existing_object (:139) | POST /9999/0/0 -> 4.04 | yes | |
| ExecuteTest#cannot_execute_security_object (:151) | POST /0/0/0 -> 4.04 | yes | |
| ExecuteTest#can_execute_resource (:162) | POST /3/0/4 (reboot) no payload -> 2.04 | yes | |
| ExecuteTest#can_execute_resource_with_parameters (:174) | POST /3/0/4 payload "6" (ct=0) -> 2.04 | yes | arg syntax `<digit>[='value']` validated server-side (leshan-lwm2m-core/.../request/ExecuteRequest.java:146) |

### Create / Delete

CreateTest = TLV, JSON, SENML_JSON, SENML_CBOR x T5 (create/CreateTest.java:81). CreateFailedTest, DeleteTest = T5, default TLV. DeleteClientOnlyTest = CoAP only, client Cf|java-coap, server Cf, raw CoAP DELETE (DeleteClientOnlyTest.java:59).

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| CreateTest#can_create_instance_without_instance_id (create/CreateTest.java:125) | POST /2 with resources only (TLV) -> 2.01, Location "2/0"; again -> "2/1"; read /2 has both | yes | only TLV allowed: other formats throw InvalidRequestException locally before send (leshan-lwm2m-core/.../request/CreateRequest.java:278); server must parse Location-Path |
| CreateTest#can_create_instance_with_id (:161) | POST /2 instance 12 -> 2.01, Location null; read /2/12 | yes | no Location when id supplied |
| CreateTest#can_create_2_instances_of_object (:181) | POST /2 with instances 12,13 in one payload -> 2.01; both readable | yes | multi-instance create (1.1+) |
| CreateTest#cannot_create_instance_without_all_required_resources (:204) | POST /2 empty instance -> 4.00; 1 mandatory only -> 4.00; 2 instances, one incomplete -> 4.00; GET /2/0 -> 4.04 | yes | create is atomic: partial-valid batch creates nothing |
| CreateFailedTest#cannot_create_mandatory_single_object (create/CreateFailedTest.java:99) | POST /3 -> 4.00 | yes | single-instance object |
| CreateFailedTest#cannot_create_instance_of_security_object (:112) | POST /0 -> 4.04 | yes | |
| CreateFailedTest#cannot_create_instance_of_absent_object (:124) | POST /50 empty -> 4.04 | yes | |
| DeleteTest#delete_created_object_instance (DeleteTest.java:105) | create /2/0, DELETE /2/0 -> 2.02 | yes | |
| DeleteTest#cannot_delete_unknown_object_instance (:121) | DELETE /2/0 (absent) -> 4.04 | yes | |
| DeleteTest#cannot_delete_device_object_instance (:133) | DELETE /3/0 -> 4.05 | yes | |
| DeleteTest#cannot_delete_security_object_instance (:145) | DELETE /0/0 -> 4.04 | yes | |
| DeleteClientOnlyTest#cannot_delete_resource (DeleteClientOnlyTest.java:99) | raw CoAP DELETE /2/0/0 -> 4.00 | client-only | LwM2M API forbids non-instance Delete locally (leshan-lwm2m-core/.../request/DeleteRequest.java:76) |

### Observe / Cancel

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| ObserveTest (T5) IT/observe/ObserveTest.java:85 | matrix | | |
| ObserveTest#can_observe_resource IT/observe/ObserveTest.java:132 | Observe GET /3/0/15 -> 2.05; observation path=/3/0/15, regId=current reg; store holds exactly 1 obs; server Write 3/0/15 -> 2.04 triggers notification w/ new value | yes | notification carries single resource node; observation keyed by reg id |
| ObserveTest#can_observe_resource_instance :160 | Observe /TEST_OBJECT/0/MULTIPLE_STRING/0 (resource instance) -> 2.05; Write TLV REPLACE instance -> 2.04; notification = resource instance node | yes | 4-level path observe |
| ObserveTest#observe_resource_instance_then_delete_it :189 | observe res-instance; Write REPLACE multi-resource with empty map -> 2.04 (instance disappears); client sends notification 4.04 NOT_FOUND and observation is cancelled on server | yes | error-code notification terminates relation; skipped for java-coap (java-coap#76) |
| ObserveTest#can_observe_resource_instance_then_passive_cancel :224 | after 1 notification, server-local `cancelObservation` -> cancelled event within 500ms, store empty; next write -> 2.04, no notification within 1s | yes | passive cancel = forget token; next notify from client is rejected (RST) |
| ObserveTest#can_observe_resource_instance_then_active_cancel :267 | CancelObservationRequest (GET Observe=1 same token) -> 2.05 with current value "a new string"; store **still contains** observation; next write -> no notification | yes | active cancel does NOT remove from store (must be done separately) |
| ObserveTest#can_observe_resource_then_passive_cancel :316 | same as passive cancel on /3/0/15; then 2 more writes both 2.04 | yes | client must survive RST of stale notification (second write still OK) |
| ObserveTest#can_observe_resource_then_active_cancel :361 | active cancel on /3/0/15 -> 2.05 "Europe/Paris"; obs remains in store; no further notification | yes | |
| ObserveTest#can_observe_instance :407 | Observe /3/0 -> 2.05; write 3/0/15 -> notification is ObjectInstance, equal to subsequent Read /3/0 | yes | notification on parent path contains full instance |
| ObserveTest#can_observe_instance_then_delete_it :439 | Observe /TEST/0; Delete /TEST/0 -> 2.02; notification 4.04 then observation cancelled | yes | skipped for java-coap |
| ObserveTest#can_observe_object :473 | Observe /3 -> 2.05; write -> notification is LwM2mObject == Read /3 | yes | |
| ObserveTest#can_observe_then_delete_it :505 | Observe /TEST; client removes object enabler -> notification 4.04, cancelled | yes | object removal client-side; skipped java-coap |
| RedisObserveTest IT/observe/RedisObserveTest.java:21 | reruns all 11 ObserveTest methods with `withRedisRegistrationStore()` | yes | observation persisted in redis |
| ObserveServerOnlyTest (S2)#can_handle_error_on_notification IT/observe/ObserveServerOnlyTest.java:112 | raw NON 2.05 notification w/ observe=2, valid token, Content-Format 666 (unsupported) -> server emits notification **error** (onError), observation stays | yes | undecodable notification -> error event, not crash; injected via TestObserveUtil (IT/observe/TestObserveUtil.java:34, NON, random MID) |
| ObserveTimeStampTest (S2 x {JSON,SENML_JSON,SENML_CBOR})#can_observe_timestamped_resource IT/observe/ObserveTimeStampTest.java:119 | Observe w/ requested format; raw notification w/ 2 timestamped values (t, t-2ms) -> response content = most recent node; getTimestampedLwM2mNodes = full list in order | yes | timestamped single-obs notifications; content format must match requested |
| ObserveTimeStampTest#can_observe_timestamped_instance :161 | same for /3/0 with 3 timestamped instances | yes | |
| ObserveTimeStampTest#can_observe_timestamped_instance_with_null :207 | first entry has **null timestamp**, others timestamped -> content = null-ts node treated as most recent; list preserved | yes | null ts = "now"/most recent; mixed ts/no-ts payload accepted |
| ObserveTimeStampTest#can_observe_timestamped_object :252 | same for /3 object | yes | |
| DynamicIPObserveTest (COAP 4 combos no-TLS; COAPS cf->cf TLS) IT/observe/DynamicIPObserveTest.java:78 | matrix; client behind ReverseProxy | | |
| DynamicIPObserveTest#can_not_send_notification_if_client_ip_changes :124 | NoSec: observe ok; proxy changes client-side source address; client-side write -> notification **ignored** (none within 1s) | yes | no-sec identity = IP:port; notifications from new address dropped |
| DynamicIPObserveTest#can_send_notification_if_ip_changes_using_oscore :172 | @Disabled, empty | no | TODO OSCORE |
| DynamicIPObserveTest#can_send_notification_if_ip_changes_using_psk :178 | PSK: observe, address change + new DTLS handshake -> notification accepted; registration object **unchanged** (address not updated) | yes | identity = PSK id survives IP change |
| DynamicIPObserveTest#update_registration_on_notification_using_psk :209 | server `withUpdateOnNotification()`: same flow -> registration socket address updated to new one | yes | optional non-spec mode: notification updates reg address (authorizer-checked) |
| DynamicIPObserveTest#can_send_notification_if_ip_changes_using_rpk :241 | RPK: notification accepted, reg unchanged | yes | |
| DynamicIPObserveTest#can_send_notification_if_ip_changes_using_x509 :273 | X509 (server-only role, trusted root): notification accepted, reg unchanged | yes | |
| RedisDynamicIPObserveTest IT/observe/RedisDynamicIPObserveTest.java:21 | reruns DynamicIPObserveTest w/ redis reg store | yes | |
| ObservationServiceTest#observe_twice_cancels_first CF/observation/ObservationServiceTest.java:90 | 2 observations same reg + same path (diff tokens) -> only 1 kept | yes | new observe on same path replaces old (SRVm/registration/InMemoryRegistrationStore.java:259-265) |
| ObservationServiceTest#cancel_by_client :102 | cancelObservations(reg) removes 2 of reg, returns 2; other client's untouched | yes | |
| ObservationServiceTest#cancel_by_path :125 | 3 adds (2 same path -> collapses) => 2 obs; cancel "/3/0/12" -> 1 cancelled, 1 left | yes | |
| ObservationServiceTest#cancel_by_observation :149 | cancel specific obs -> 1 remains | yes | |
| ObservationServiceTest#assertEqualsHashcode :272 | Observation equals/hashCode ignore protocolData | yes | identity = token+endpointUri+regId+paths |
| LwM2mObservationStoreTest#put_coap_observation_with_valid_request CF/observation/LwM2mObservationStoreTest.java:117 | CoAP obs put/get by token round-trips | yes | store needs existing registration |
| LwM2mObservationStoreTest#get_observation_from_request :135 | stored obs retrievable as SingleObservation by (regId, endpointUri+token) | yes | ObservationIdentifier = server endpoint URI + token |
| LwM2mObservationStoreTest#get_composite_observation_from_request :155 | FETCH observe -> CompositeObservation with ordered paths | yes | |
| LwM2mObservationStoreTest#remove_observation :175 | remove(token) -> getObservation null | yes | |
| ObserveUtilTest#should_create_observation_from_context CF/observation/ObserveUtilTest.java:47 | userContext (path, regId, extra keys) + token + Accept -> SingleObservation fields, extra ctx kept | yes | app context propagated into observation |
| ObserveUtilTest#should_create_composite_observation_from_context :77 | composite: request CF=CBOR, response CF=JSON retained separately | yes | composite obs has two content formats |
| ObserveUtilTest#should_not_create_observation_without_context :109 | no ctx -> IllegalStateException | yes | |
| ObserveUtilTest#should_not_create_observation_without_path_in_context :121 | ctx w/o path -> IllegalStateException | yes | |
| LwM2mResponseBuilderTest#visit_observe_request CF/request/LwM2mResponseBuilderTest.java:52 | 2.05 + Observe option -> ObserveResponse with SingleObservation(path) | yes | observation only created when response has Observe option |
| CoapRequestBuilderTest#build_observe_request CF/request/CoapRequestBuilderTest.java:339 | ObserveRequest(12,0) -> GET, Observe=0, coap://peer/12/0 | yes | |

### Composite ops

_From group B:_

ReadCompositeTest = {req,resp} fmt pairs (SENML_JSON,SENML_JSON), (CBOR,CBOR), (CBOR,JSON), (JSON,CBOR) x T5 (read/ReadCompositeTest.java:78). WriteCompositeTest = SENML_JSON, SENML_CBOR x T5. Read-Composite = FETCH / with Content-Format=req fmt + Accept=resp fmt (CoapRequestBuilder.java:199); Write-Composite = iPATCH / (CoapRequestBuilder.java:249).

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| ReadCompositeTest#can_read_root (read/ReadCompositeTest.java:125) | FETCH ["/"] -> 2.05 resp fmt; LwM2mRoot objects == object ids of registration's available instances; each instance equals plain Read SENML_JSON | yes | root path in composite; result must match registration's object links |
| ReadCompositeTest#can_read_resources (:175) | FETCH ["/3/0/0","/1/0/1"] -> 2.05; string + integer resources | yes | cross-object |
| ReadCompositeTest#can_read_resource_instance (:197) | FETCH [/3442/0/1110/0] -> 2.05 resource instance | yes | |
| ReadCompositeTest#can_read_resource_and_instance (:217) | FETCH ["/3/0/0","/1"] -> 2.05; resource + object with 1 instance | yes | mixed depth paths |
| WriteCompositeTest#can_write_resources (write/WriteCompositeTest.java:126) | iPATCH {/3/0/14:"+02", /1/0/2:100} -> 2.04; reads confirm | yes | |
| WriteCompositeTest#can_write_resource_and_instance (:149) | iPATCH resource + resource instance -> 2.04; read back | yes | |
| WriteCompositeTest#can_add_resource_instances (:180) | iPATCH new /3442/0/1110/1 -> 2.04; resource now has 2 instances | yes | composite on res-instance adds, not replaces |
| WriteCompositeTest#can_observe_instance_with_composite_write (:210) | Observe /3/0 2.05 (observation regId matches); iPATCH SENML_CBOR /3/0/14,/3/0/15 -> 2.04; exactly ONE notification containing both new values; none within 1s after | yes | server must not expect per-resource notifications |

_From group C:_

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| ObserveCompositeTest (T5) IT/observe/ObserveCompositeTest.java:77 | matrix; all SENML_JSON req+resp | | |
| ObserveCompositeTest#can_composite_observe_on_single_resource :120 | FETCH+Observe ["/3/0/15"] -> 2.05; CompositeObservation paths={/3/0/15}, regId ok, 1 obs in store; write -> notification map {/3/0/15: new value} | yes | |
| ObserveCompositeTest#should_not_get_response_if_modified_other_resource_than_observed :154 | observe /3/0/14; write /3/0/15 -> no notification 1s | yes (client-side filter) | |
| ObserveCompositeTest#can_composite_observe_on_multiple_resources :185 | observe [/3/0/15,/3/0/14]; write one -> notification contains **both** paths (unchanged one = previous value) | yes | composite notify always full set |
| ObserveCompositeTest#can_composite_observe_on_multiple_resources_with_write_composite :222 | WriteComposite (iPATCH SENML_JSON) both -> 2.04; single notification with both new values | yes | |
| ObserveCompositeTest#can_composite_observe_on_root :261 | observe ["/"] -> root node with exactly reg's available objects/instances, each == Read SENML_JSON of instance; same after write-triggered notification | yes | root path composite observe; server uses registration availableInstances |
| ObserveCompositeTest#can_observe_instance :328 | composite /3/0 -> notification {/3/0: == Read /3/0} | yes | |
| ObserveCompositeTest#can_observe_object :363 | composite /3 -> notification {/3: == Read /3} | yes | |
| ObserveCompositeTest#can_passive_cancel_composite_observation :398 | local cancel -> store empty, cancelled event, no notification 500ms | yes | |
| ObserveCompositeTest#can_active_cancel_composite_observation :425 | CancelCompositeObservationRequest -> 2.05; obs stays in store; no further notification | yes | active cancel keeps store entry (comment :449) |
| ObserveCompositeTimeStampTest (S2 x {SENML_JSON,SENML_CBOR}) IT/observe/ObserveCompositeTimeStampTest.java:75 | matrix; raw injected notifications | | |
| ObserveCompositeTimeStampTest#can_observecomposite_timestamped_resource :124 | observe [/1/0/1,/3/0/15]; notif with 2 timestamps (t1, t1-2000s) -> content = most-recent nodes; timestamped nodes equal | yes | observation paths order preserved (containsExactlyElementsOf) |
| ObserveCompositeTimeStampTest#can_observecomposite_timestamped_resource_with_empty_value :147 | some (ts,path) entries null -> still decoded and equal | yes | sparse timestamped data |
| ObserveCompositeTimeStampTest#can_observecomposite_timestamped_instance :170 | [/1/0/1,/3/0] full device instance timestamped | yes | |
| ObserveCompositeTimeStampTest#can_observecomposite__timestamped_object :208 | [/1/0/1,/3] object | yes | |
| ObserveCompositeTimeStampTest#reject_observecomposite_response_for_unexpected_resource_path :281 | notification also carries /1/0/0 (not observed) -> notification error InvalidResponseException | yes | server rejects notification containing paths outside the observation |
| ObserveCompositeTimeStampTest#reject_observecomposite_response_for_unexpected_instance_path :307 | observed /3/0 but notif contains /3/1 -> InvalidResponseException | yes | prefix check: child-of-observed only |
| LwM2mResponseBuilderTest#visit_observe_composite_request CF/request/LwM2mResponseBuilderTest.java:82 | 2.05+Observe -> CompositeObservation with paths | yes | |

### Send

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| SendTest (T5 x {SENML_JSON,SENML_CBOR}) IT/send/SendTest.java:69 | matrix | | |
| SendTest#can_send_resources :121 | client POST /dp [/3/0/1,/3/0/2] -> success (2.04); server data listener gets most-recent nodes {model "IT-TEST-123", serial "12345"} | yes | |
| SendTest#can_send_resources_asynchronously :143 | same via async callback: 1 success response, no error | yes | |
| RedisSendTest IT/send/RedisSendTest.java:26 | reruns SendTest with RedisRegistrationStore | yes | |
| SendTimestampedTest (T5 x SENML_JSON/CBOR)#server_handle_multiple_timestamped_node IT/send/SendTimestampedTest.java:114 | client collects /TEST/0/FLOAT twice 1s apart, sends once -> server data has 2 non-null timestamps, each with FLOAT resource | yes | multi-timestamp Send payload |
| LockStepSendTest (S2 server only)#register_send_with_invalid_payload IT/send/LockStepSendTest.java:139 | raw client Register -> 2.01; Send SENML_CBOR with garbage payload {0x00,0x10} -> server send-error listener fires | yes | server replies 4.00 "Invalid Payload" (CFm/send/SendResource.java:112-117); retransmission tuned to ~1s timeout (:97-100, :114) |
| DynamicIPSendTest (COAP 4 combos; COAPS cf->cf) IT/send/DynamicIPSendTest.java:77 | matrix; ReverseProxy | | |
| DynamicIPSendTest#can_not_send_if_client_ip_changes :123 | NoSec: proxy address change -> Send response 4.00 BAD_REQUEST | yes | no reg found for new IP identity (CFm/send/SendResource.java:69-72) |
| DynamicIPSendTest#can_send_if_client_ip_changes_using_oscore :155 | @Disabled | no | TODO |
| DynamicIPSendTest#can_send_if_client_ip_changes_using_psk :161 | PSK + new handshake from new IP -> Send success, data received; registration unchanged | yes | |
| DynamicIPSendTest#update_registration_on_send_using_psk :192 | `withUpdateOnSendOperation()` -> reg socket address updated after Send | yes | non-spec option (SRVm/send/SendHandler.java:119-154) |
| DynamicIPSendTest#can_send_if_client_ip_changes_using_rpk :223 | RPK: Send ok after IP change | yes | |
| DynamicIPSendTest#can_send_if_client_ip_changes_using_x509 :250 | X509: Send ok after IP change | yes | |
| RedisDynamicISendTest IT/send/RedisDynamicISendTest.java:26 | reruns DynamicIPSendTest w/ redis | yes | |

### Block-wise

No dedicated tests in part-C scope. Related: LockStepSendTest configures CoAP retransmission so an un-ACKed CON request times out in ~1s (cf: ACK_TIMEOUT 200ms, scale 1, MAX_RETRANSMIT 4, IT/send/LockStepSendTest.java:97-100; jc: exponential 140ms x2, 1 retry, :114). java-coap notification path fetches remaining blocks of block-wise notifications (JCm/observation/CoapNotificationReceiver.java:98-99).

### Bootstrap

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| BootstrapTest#bootstrap `IT/bootstrap/BootstrapTest.java:131` | Client POST /bs?ep=X -> 2.04; BS server sends Write /0/0,/0/1,/1/0 then Bootstrap-Finish; client then Registers at DM server (new registration seen). | yes | Happy path; sequence = deletes, security writes, server writes, ACL writes, OSCORE writes, Finish (`leshan-lwm2m-bsserver/.../BootstrapUtil.java:217-241`). |
| BootstrapTest#bootstrap_without_endpoint_name `:162` | Bootstrap-Request w/o `ep` over plain CoAP -> 4.00 BAD_REQUEST. | yes | No identity to derive ep from -> `badRequest("endpoint name missing")` (`DefaultBootstrapHandler.java:91-97`). |
| BootstrapTest#bootstrap_tlv_only `:185` | Client sends no preferred content format, supports only TLV -> every BS Write uses TLV; registration succeeds. | yes | Default CF when no `pct` in request = TLV (`DefaultBootstrapSession.java:107-111`). |
| BootstrapTest#bootstrap_senmlcbor_only `:225` | Client prefers SENML_CBOR -> all BS Writes encoded SenML-CBOR. | yes | Server honors Bootstrap-Request `pct` (preferred content format). |
| BootstrapTest#bootstrap_contentformat_from_config `:266` | Client prefers SENML_CBOR, config forces SENML_JSON -> Writes use SENML_JSON. | yes | Config CF overrides client preference (`BootstrapConfigStoreTaskProvider.java:81,86`). |
| BootstrapTest#bootstrapWithAdditionalAttributes `:307` | Bootstrap-Request with extra query params (key1=value1, imei=...) -> stored on session `getBootstrapRequest().getAdditionalAttributes()`. | yes | Unknown query params must be kept, not rejected. |
| BootstrapTest#bootstrapWithDiscoverOnRoot `:350` | BS server first sends Bootstrap-Discover `/` -> 2.05 with `</>;lwm2m=1.0,</0/0>;uri="coap://h:p",</1>,</2>,</3442/0>,</3/0>`. | yes | Custom first request before config; parse lwm2m= version and `uri` attr in link format. |
| BootstrapTest#bootstrapWithDiscoverOnRootThenRebootstrap `:393` | After bootstrap+register, server Executes /1/0/9 (Bootstrap-Request Trigger) -> 2.04 (or RequestCanceled); client deregisters, re-bootstraps; 2nd Discover returns `</0/1>;ssid=2222;uri=...,</1/0>;ssid=2222` too. | yes | Execute response may race with client Deregister -> pending request cancelled; DM must tolerate. Links carry `ssid` per security/server instance. |
| BootstrapTest#bootstrapWithDiscoverOnDevice `:462` | Bootstrap-Discover /3 -> 2.05 `</>;lwm2m=1.0,</3/0>`. | yes | Object-level discover still includes `</>;lwm2m=`. |
| BootstrapTest#bootstrap_create_2_bsserver `:502` | Client already has BS account at /0/10; config writes BS account at /0/0 (no delete) -> client Bootstrap-Finish fails (client InvalidStateException), BS server failure cause FINISH_FAILED. | yes | Two BS-server Security instances is invalid; error response to Finish -> session failed (`DefaultBootstrapSessionManager.java:169-179`, `DefaultBootstrapHandler.java:206-207`). |
| BootstrapTest#bootstrap_with_auto_id_for_security_object `:535` | Same as above but `autoIdForSecurityObject` -> BS server first Discover, finds /0/x without ssid (=10), writes BS security to /0/10 and DM security to other id; success. | yes | Find BS instance = /0/x link w/o `ssid` (`BootstrapConfigStoreTaskProvider.java:104-113`); renumber non-BS ids skipping it (`BootstrapUtil.java:251-260`). Discover error or no BS instance -> no config -> Finish only. |
| BootstrapTest#bootstrap_delete_access_control_and_connectivity_statistics `:569` | Config deletes /2 and test object -> BS Delete /2, /<test>; those objects empty after; /3, /0, /6 untouched. | yes (sends) / client applies | Delete per-object paths before writes. |
| BootstrapTest#bootstrapDeleteAll `:628` | Config `toDelete=["/"]` -> BS Delete `/`; afterwards only /3/0 and the BS /0 instance (res 1=true) remain. | yes (sends) / client semantics | Delete `/` must not delete Device or BS security (client rule; server just sends it). Then Finish with no DM account still succeeds. |
| BootstrapTest#bootstrapWithAcl `:674` | BS Writes /2/0 (obj 3, inst 0, ACL {3333:1}, owner 2222) and /2/1 (obj 4, owner 2222, no ACL map); client /2 instances match. | yes | ACL resource 2 is multi-instance keyed by ssid; ACL optional. |
| SecureBootstrapTest#bootstrap_using_psk `IT/bootstrap/SecureBootstrapTest.java:90` | DTLS-PSK to BS server (coaps), BS security store holds PSK for ep -> bootstrap ok -> client registers to plain-CoAP DM server. | yes | BS authorizer checks identity vs `getAllByEndpoint(ep)` (`DefaultBootstrapAuthorizer.java:43-55`). |
| SecureBootstrapTest#bootstrap_using_psk_without_endpointname `:124` | Client named = PSK id, omits `ep` -> BS server uses PSK identity as ep; success. | yes | ep derivation: PSK id / X509 CN / OSCORE RID-as-UTF8 (`leshan-lwm2m-servers-shared/.../DefaultServerEndpointNameProvider.java:49-66`). |
| SecureBootstrapTest#bootstrap_failed_using_bad_psk `:159` | BS store has wrong PSK key for identity -> DTLS handshake fails; client never registers to DM. | yes | Failure at handshake, no CoAP response. |
| SecureBootstrapTest#bootstrap_using_rpk `:190` | DTLS-RPK to BS, store has client RPK -> success. | yes | |
| SecureBootstrapTest#bootstrap_using_rpk_without_endpoint `:226` | RPK + no `ep` -> 4.00 BAD_REQUEST. | yes | RPK cannot yield an ep name (`DefaultBootstrapHandler.java:94-96`). Note DM Register gives 4.03 in same case (see RPK). |
| SecureBootstrapTest#bootstrap_using_x509 `:262` | DTLS-X509 to BS (cert signed by root), store has X509 info for ep -> success. | yes | |
| SecureBootstrapTest#bootstrap_using_x509_without_endpoint `:302` | X509, `ep` only if needed (CN==ep) -> BS derives ep from CN; success. | yes | |
| SecureBootstrapTest#bootstrap_using_x509_with_sni `:343` | BS server has 2 certs by SNI (localhost, virtualhost.org); client sends SNI virtualhost.org, trusts root (trust-anchor usage) -> success. | yes | Server must select cert by SNI. |
| SecureBootstrapTest#bootstrap_unsecure_then_register_to_server_using_psk `:387` | Plain-CoAP bootstrap delivering PSK creds; DM coaps server with matching PSK info -> registration. | yes | |
| SecureBootstrapTest#bootstrap_unsecure_then_register_to_server_using_rpk `:419` | Plain bootstrap delivering RPK creds + server pubkey -> DM coaps RPK registration. | yes | |
| OscoreBootstrapTest#bootstrapUnsecuredToServerWithOscore `IT/oscore/OscoreBootstrapTest.java:114` | Plain bootstrap writes DM security + /21 OSCORE object; client registers to DM via OSCORE. | yes | OSCORE object written last (`BootstrapUtil.java:237-239`); security instance links to /21. |
| OscoreBootstrapTest#bootstrapViaOscoreToServerWithOscore `:147` | Bootstrap over OSCORE (BS store has OSCORE info) then DM registration over OSCORE. | yes | Separate OSCORE contexts for BS and DM. |
| OscoreBootstrapTest#bootstrapViaOscoreToUnsecuredServer `:186` | Bootstrap over OSCORE, DM is NoSec -> registration. | yes | |
| OscoreBootstrapTest#bootstrap_via_oscore_to_unsecured_server_without_endpoint `:221` | Bootstrap over OSCORE, ep omitted -> ep = OSCORE recipient id (client sender id) decoded UTF-8; success. | yes | Non-UTF8 RID -> null -> 4.00. |
| BootstrapHandlerTest#error_if_not_authorized `BS/BootstrapHandlerTest.java:62` | Unauthorized session -> Bootstrap response 4.00 BAD_REQUEST. | yes | Unauthorized = 4.00, not 4.03 (`DefaultBootstrapHandler.java:105-109`). |
| BootstrapHandlerTest#bootstrap_success `:80` | Empty config, all requests 2.xx -> session `end` called, not `failed`. | yes | Empty config still sends Finish. Requests start only after 2.04 is sent (`:131-134`). |
| BootstrapHandlerTest#bootstrap_failed_because_of_sent_failure `:104` | Delete/Write/Finish all return 5.00 -> failure cause FINISH_FAILED. | yes | Error on Delete/Write is NOT fatal; session continues; only Finish error fails (`DefaultBootstrapSessionManager.java:162-180`). Transport error on any request -> fail (REQUEST_FAILED, `:183-191`). |
| BootstrapHandlerTest#two_bootstrap_at_the_same_time_not_allowed `:128` | 1st session hangs; 2nd Bootstrap-Request for same ep (diff port) -> 2.04 and completes; 1st session failed with CANCELLED, its pending request cancelled. | yes | One session per ep; newest wins (`DefaultBootstrapHandler.java:113-120`). |
| BootstrapConfigTest#test_toString `BS/BootstrapConfigTest.java:35` | CipherSuiteId(2 bytes) toString "0xC0,0xA8". | no (config model) | |
| BootstrapConfigTest#test_create_new_CipherSuiteId_from_ULong `:54` | ULong 0..65535 -> 2-byte id. | no | |
| BootstrapConfigTest#test_getValueForSecurityObject `:68` | round-trip ULong for /0/x/16 cipher suite. | yes (encoding of res 16) | |
| BootstrapConfigTest#test_error_thrown_for_invalid_ulong `:85` | >65535 -> IllegalArgumentException. | yes (validation) | Cipher suite is 16-bit. |
| DefaultConfigurationCheckerTest#test_valid_configuration_using_certificate_with_der_endoding `BS/DefaultConfigurationCheckerTest.java:36` | X509 config with DER cert valid. | yes | |
| DefaultConfigurationCheckerTest#test_invalid_configuration_using_certificate_with_pem_endoding `:43` | PEM cert in /0/x/3 -> InvalidConfigurationException. | yes | Certs in Security object must be DER. |
| InMemoryBootstrapConfigStoreTest#assertEqualsHashcode `BS/InMemoryBootstrapConfigStoreTest.java:24` | PskByServer equals/hashCode contract. | no | |
| LeshanBootstrapServerBuilderTest#create_server_with_default_californiumEndpointsProvider `CFBS/LeshanBootstrapServerBuilderTest.java:106` | default -> 1 COAP endpoint. | config | |
| LeshanBootstrapServerBuilderTest#create_server_without_securityStore `:115` | coap+coaps providers but no security store -> only COAP endpoint. | config | No store => no DTLS endpoint. |
| LeshanBootstrapServerBuilderTest#create_server_with_securityStore `:127` | with store -> COAP + COAPS. | config | |
| LeshanBootstrapServerBuilderTest#create_server_with_coaps_only `:156` | coaps only -> 1 COAPS. | config | |
| LeshanBootstrapServerBuilderTest#create_server_without_psk_cipher `:184` | only ECDHE_ECDSA cipher + RPK keys -> COAPS endpoint builds. | config | PSK store not required if no PSK cipher. |
| LeshanBootstrapServerTest#testStartStopStart `CFBS/LeshanBootstrapServerTest.java:76` | start/stop/start no exception. | lifecycle | |
| LeshanBootstrapServerTest#testStartDestroy `:90` | destroy releases all threads (after a bootstrap). | lifecycle | |
| LeshanBootstrapServerTest#testStartStopDestroy `:109` | stop then destroy releases threads. | lifecycle | |

### Security PSK

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| PskTest#registered_device_with_psk_to_server_with_psk `IT/security/PskTest.java:113` | DTLS-PSK handshake ok, Register 2.01, server Read /3/0/1 -> success. | yes | |
| PskTest#registered_device_with_psk_to_server_with_psk_without_endpointname `:144` | Register w/o `ep`, PSK id = ep -> registered under PSK id. | yes | DM ep derivation (`RegistrationHandler.java:67-73`). |
| PskTest#register_update_deregister_reregister_device_with_psk_to_server_with_psk `:177` | lifetime 2s: Register, Update, Deregister (2.02), Register again (new reg). | yes | Auth check on Update/Deregister too (`DefaultAuthorizer.java:81-112`). |
| PskTest#register_update_reregister_device_with_psk_to_server_with_psk `:224` | stop w/o deregister; still registered after 500ms; restart -> re-registration (replaces old reg). | yes | Same ep re-register = replace, fires "re-registration". |
| PskTest#server_initiates_dtls_handshake `:269` | Server drops DTLS state; server Read /3 -> server initiates handshake (it is DTLS client role) and succeeds. | yes | Non-queue reg: handshake mode AUTO (`DefaultCoapsIdentityHandler.java:92-96`); PSK identity for server-initiated handshake looked up via registration by address (`LwM2mPskStore.java:getIdentity`). DTLS role BOTH (`CoapsServerEndpointFactory.java:86`). |
| PskTest#server_initiates_dtls_handshake_timeout `:303` | Session cleared + client stopped; async Read timeout 1000ms -> TimeoutException type DTLS_HANDSHAKE_TIMEOUT, no response callback. | yes | Distinct timeout type for handshake vs CoAP response. |
| PskTest#server_does_not_initiate_dtls_handshake_with_queue_mode `:350` | Queue-mode client, session cleared -> send throws UnconnectedPeerException immediately; client marked sleeping. | yes | Queue mode: never initiate handshake (`Registration.java:294-297`, default handshake NONE `CoapsServerEndpointFactory.java:85`). Failure flips presence to sleeping. |
| PskTest#registered_device_with_bad_psk_identity_to_server_with_psk `:391` | Store has other PSK id for ep -> unknown identity -> handshake fails; not registered. | yes | |
| PskTest#registered_device_with_bad_psk_key_to_server_with_psk `:416` | Wrong id+key -> handshake fails. | yes | |
| PskTest#registered_device_with_psk_and_bad_endpoint_to_server_with_psk `:441` | PSK id belongs to BAD_ENDPOINT; client registers as ep X -> handshake ok but Register rejected (4.03); not registered. | yes | Identity/ep mismatch: store lookup by ep X has no info but identity is secure -> declined (`SecurityChecker.java:88-93`, `RegistrationHandler.java:104-107`). |
| PskTest#registered_device_with_psk_identity_to_server_with_psk_then_remove_security_info `:466` | `remove(ep, compromised=true)` -> DTLS connection terminated server side; client Update times out. | yes | Compromised removal kills DTLS sessions for that principal (`CoapsServerEndpointFactory.java:312-325`, `ConnectionCleaner.java:42-78`). |
| ServerOnlySecurityTest#dont_sent_request_if_identity_change `IT/security/ServerOnlySecurityTest.java:168` | After registration, client re-handshakes with different PSK id "anotherPSK" (also in store); server Read -> SendFailedException caused by EndpointMismatchException. | yes | Server must bind downlink to the registration's identity; if current DTLS peer identity != registered identity, refuse to send. |
| SecurityStoreTest#nonunique_psk_identity `IT/security/SecurityStoreTest.java:68` | Adding same PSK id for 2nd ep -> NonUniqueSecurityInfoException. | yes | PSK id is a unique index (`InMemorySecurityStore.java:105-113`). |
| SecurityStoreTest#change_psk_identity_cleanup `:79` | Re-add ep with new PSK id frees old id for another ep. | yes | Replace must drop old index entries. |

### Security RPK

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| RpkTest#registered_device_with_rpk_to_server_with_rpk `IT/security/RpkTest.java:102` | DTLS-RPK both sides, Register ok, Read /3/0/1 ok. | yes | |
| RpkTest#registered_device_with_rpk_to_server_with_rpk_without_endpointname `:134` | RPK, no `ep` -> Register 4.03 FORBIDDEN. | yes | `forbidden("endpoint name missing")` (`RegistrationHandler.java:72`). |
| RpkTest#registered_device_with_bad_rpk_to_server_with_rpk `:164` | Store has different pubkey for ep -> not registered. | yes | Handshake may succeed (RPK trust is per-store); rejection at Register via key compare (`SecurityChecker.java:149-175`). |
| RpkTest#registered_device_with_rpk_to_server_with_rpk_then_remove_security_info `:190` | remove compromised -> connection cleaned; Update times out. | yes | Cleaner matches RPK by key equality. |
| RpkTest#registered_device_with_rpk_and_bad_endpoint_to_server_with_rpk `:224` | Key registered for BAD_ENDPOINT, client uses ep X -> not registered. | yes | |
| RpkX509Test#registered_device_with_x509cert_to_server_with_rpk `IT/security/RpkX509Test.java:101` | Server RPK-only, client presents X509 (store has RPK of cert pubkey) -> not registered. | yes | Cert-type mismatch -> handshake/identity reject even if key same. |
| RpkX509Test#registered_device_with_rpk_to_server_with_x509cert `:120` | Server has X509 cert (server-only role), client RPK trusting server cert pubkey -> registered; Read ok. | yes | Server must offer both RPK and X509 cert types; X509 server can accept RPK client. |
| SniTest#registered_device_with_rpk `IT/security/SniTest.java:113` | Server 2 RPK keypairs by SNI; client SNI virtualhost.org trusts 2nd key -> registered, Read ok. | yes | Select key by SNI; downlinks must reuse virtual host (`DefaultCoapsIdentityHandler.java:89`). |

### Security X509

All X509Test rows: server `actingAsServerOnly` (DTLS role SERVER_ONLY), runs COAPS + COAPS_TCP. "Usage" = client-side certificate usage (RFC 6698 style, /0/x/15). Failures asserted only as "not registered after 1s".

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| X509Test#registered_device_with_x509cert_to_server_with_x509cert_then_remove_security_info `IT/security/X509Test.java:115` | remove(ep, compromised) -> COAPS: 1st Update times out, 2nd Update fails; TCP: Update fails directly. | yes | Cleaner on DTLS (`ConnectionCleaner.java`) and TCP close (`JavaCoapsTcpServerEndpointsProvider.java:126-135`). After reconnect, Register/Update rejected (no security info but secure identity). |
| ...#registered_device_with_x509cert_to_server_with_x509cert `:163` | mutual X509, Register ok, Read ok. | yes | |
| ...#registered_device_with_x509cert_to_server_with_x509cert_without_endpointname `:199` | no `ep` -> ep = cert CN; ok. | yes | |
| ...#registered_device_with_x509cert_to_server_with_self_signed_x509cert `:236` | server self-signed cert, client trusts it directly -> ok. | client-mostly | |
| ...#registered_device_with_x509cert_and_bad_endpoint_to_server_with_x509cert `:271` | store has X509 info for BAD_ENDPOINT only -> rejected. | yes | |
| ...#registered_device_with_x509cert_and_bad_cn_certificate_to_server_with_x509cert `:299` | client named BAD_ENDPOINT, cert CN differs; store for BAD_ENDPOINT -> rejected. | yes | Rule: cert CN must equal ep (`SecurityChecker.java:177-200`). |
| ...#registered_device_with_x509cert_and_bad_private_key_to_server_with_x509cert `:327` | private key not matching cert -> handshake fails. | yes | |
| ...#registered_device_with_untrusted_x509cert_to_server_with_x509cert `:357` | client cert not chained to server trust store -> rejected. | yes | Server validates client chain against trust store. |
| ...#registered_device_with_selfsigned_x509cert_to_server_with_x509cert `:385` | self-signed client cert -> rejected. | yes | |
| ...#registered_device_with_x509cert_using_ca_constraint_with_direct_trust `:427` | CA_CONSTRAINT with end-entity cert as "CA" -> client rejects. | client-only | |
| ...#registered_device_with_x509cert_with_intermediate_ca_as_ca_contraint `:461` | CA_CONSTRAINT = intermediate CA in chain -> ok. | client (server must send full chain) | Server must send intermediate in chain. |
| ...#registered_device_with_x509cert_using_ca_constraint_like_trust_anchor `:501` | CA_CONSTRAINT, client truststore lacks root -> rejected. | client-only | |
| ...#registered_device_with_x509cert_with_root_as_ca_contraint `:541` | CA_CONSTRAINT = root -> ok. | client-only | |
| ...#registered_device_with_x509cert_with_server_self_signed_certificate_as_ca_contraint_which_is_not_in_certhchain `:580` | CA_CONSTRAINT = unrelated self-signed -> rejected. | client-only | |
| ...#registered_device_with_x509cert_with_server_self_signed_certificate_as_ca_contraint `:614` | server self-signed + CA_CONSTRAINT -> rejected. | client-only | |
| ...#registered_device_with_x509cert_using_service_certificate_constraint `:650` | SERVICE_CERT_CONSTRAINT = server EE cert, chain valid -> ok. | client (server chain) | |
| ...#registered_device_with_x509cert_using_root_ca_as_service_certificate_constraint `:690` | SERVICE_CERT = root -> rejected. | client-only | |
| ...#registered_device_with_x509cert_using_another_certificate_as_service_certificate_constraint `:724` | SERVICE_CERT = other EE cert same DNS -> rejected. | client-only | |
| ...#registered_device_with_x509cert_using_server_self_signed_cert_as_service_certificate_constraint_which_is_not_in_certhchain `:758` | SERVICE_CERT = self-signed not in chain -> rejected. | client-only | |
| ...#registered_device_with_x509cert_using_server_self_signed_cert_as_service_certificate_constraint `:792` | server self-signed + SERVICE_CERT -> rejected (not PKIX). | client-only | |
| ...#registered_device_with_x509cert_using_service_certificate_constraint_with_missing_intermediate_certificate_in_chain `:826` | server sends EE cert w/o intermediate -> rejected. | yes (server chain config) | Server must be configurable to send full chain. |
| ...#registered_device_with_x509cert_using_trust_anchor_assertion_with_direct_trust `:864` | TA = EE cert -> rejected. | client-only | |
| ...#registered_device_with_x509cert_with_intermediate_cert_as_trust_anchor_assertion `:898` | TA = intermediate -> ok. | client-only | |
| ...#registered_device_with_x509cert_with_root_ca_cert_as_trust_anchor_assertion `:937` | TA = root -> ok. | client-only | |
| ...#registered_device_with_x509cert_with_trust_anchor_assertion_which_is_not_in_certchain `:976` | TA = other EE cert -> rejected. | client-only | |
| ...#registered_device_with_x509cert_with_server_self_signed__cert_as_trust_anchor_assertion `:1010` | TA = self-signed not in chain -> rejected. | client-only | |
| ...#registered_device_with_x509cert_using_direct_trust_with_self_signed_certificate_as_trust_anchor_assertion `:1044` | server self-signed + TA self-signed -> rejected. | client-only | |
| ...#registered_device_with_x509cert_using_direct_trust_with_certificate_signed_by_ca_as_trust_anchor_assertion `:1078` | TA = EE cert w/o chain -> rejected. | client-only | |
| ...#registered_device_with_x509cert_with_server_certificate_signed_by_ca_as_domain_issuer_certificate `:1114` | DOMAIN_ISSUER = server EE cert -> ok. | client-only | |
| ...#registered_device_with_x509cert_using_no_end_entity_certificate_as_domain_issuer_certificate `:1153` | DOMAIN_ISSUER = root -> rejected. | client-only | |
| ...#registered_device_with_x509cert_using_another_end_entity_certificate_as_domain_issuer_certificate `:1187` | DOMAIN_ISSUER = other EE -> rejected. | client-only | |
| ...#registered_device_with_x509cert_using_unexpected_self_signed_certificate_as_domain_issuer_certificate `:1221` | DOMAIN_ISSUER = self-signed variant of same key -> rejected. | client-only | Compare whole cert, not just key. |
| ...#registered_device_with_x509cert_using_expected_self_signed_certificate_as_domain_issuer_certificate `:1255` | server self-signed + DOMAIN_ISSUER same cert -> ok. | client-only | |
| ...#registered_device_with_x509cert_using_server_certificate_signed_by_ca_as_domain_issuer_certificate `:1294` | server EE w/o chain + DOMAIN_ISSUER same -> ok. | client-only | |
| SniTest#registered_device_with_x509_using_domain_issuer_certificate_usage `IT/security/SniTest.java:152` | SNI "virtualhost-with-different-cn.org" selects 2nd cert; DOMAIN_ISSUER -> ok. | yes (SNI cert selection) | |
| SniTest#registered_device_with_x509_using_trust_anchor_assertion_certificate_usage `:193` | SNI virtualhost.org, TA=root -> ok. | yes | Hostname must match SAN for PKIX usages. |
| SniTest#registered_device_with_x509_using_service_certificate_constraint_certificate_usage `:233` | SNI + SERVICE_CERT = vhost cert -> ok. | yes | |
| SniTest#registered_device_with_x509_using_ca_constraint_certificate_usage `:273` | SNI + vhost chain with intermediate, CA_CONSTRAINT=intermediate -> ok. | yes | Per-SNI chain incl. intermediate. |

### Security OSCORE

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| OscoreTest#registered_device_with_oscore_to_server_with_oscore `IT/oscore/OscoreTest.java:82` | OSCORE-protected Register ok; server Read ok. | yes | Server ctx = (sid=client rid, rid=client sid). |
| OscoreTest#registered_device_with_oscore_to_server_with_oscore_without_endpoint_name `:107` | No `ep`; ep = recipient id as UTF-8 -> ok. | yes | |
| OscoreTest#..._then_removed_security_info_then_server_fails_to_send_request `:135` | remove(ep, compromised) -> next server Read (500ms) returns null (timeout). | yes | OSCORE ctx removed on security removal (`OscoreContextCleaner.java:78-84`). TODO in Leshan: expected behavior undefined. |
| OscoreTest#..._then_removed_security_info_then_client_fails_to_update `:168` | remove -> client Update fails within 3s. | yes | Server can't decrypt -> error (4.01 per RFC 8613). |
| OscoreTest#..._then_stop_device_then_register_again `:202` | Client restart (new seq/ctx, RFC 8613 App. B.2) w/o deregister -> re-registration; Read ok. | yes | Must support B.2 context re-derivation; cleaner keeps ctx on re-reg with same identity (`OscoreContextCleaner.java:61-74`). |
| SecurityStoreTest#nonunique_oscore_rid `IT/security/SecurityStoreTest.java:95` | Same OSCORE RID for 2nd ep -> NonUniqueSecurityInfoException. | yes | RID is unique index (`InMemorySecurityStore.java:123`). |
| SecurityStoreTest#change_oscore_rid_cleanup `:114` | Replacing ep's OSCORE setting frees old RID. | yes | |
| (OSCORE bootstrap rows in Bootstrap table) | | | |

### Redis / clustering

_From group A:_

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| RedisRegistrationTest `IT/RedisRegistrationTest.java` | Re-runs all 6 RegistrationTest methods (×5 transports) with RedisRegistrationStore | yes | Includes cancel-pending-on-reregister through Redis |
| RedisRegistrationStoreTest#get_observation_from_request `IT/server/redis/RedisRegistrationStoreTest.java:126` | Cf observation put into the store → readable as SingleObservation by (regId, (endpointUri, token)) with the right path | yes | Observation is stored with its endpoint URI |
| …#get_composite_observation_from_request `:148` | FETCH-based composite observation → CompositeObservation with the same paths in order | yes | |
| …#remove_observation `:170` | Remove by token → lookup returns null | yes | |
| RegistrationSerDesTest#ser_and_des_are_equals `RED/serialization/RegistrationSerDesTest.java:48` | Registration round-trips, including link attributes of every kind (unquoted, quoted, rt, ct list, valueless `hb`) | yes (persistence) | Keep the attribute *kind* (quoted/unquoted/valueless) |
| …#ser_and_des_are_equals_with_app_data `:82` | Round-trip with custom data containing a null value | yes | null values in app data survive |
| SecurityInfoSerDesTest#security_info_psk_ser_des_then_equal `RED/serialization/SecurityInfoSerDesTest.java:45` | PSK → `{"ep","id","psk":hex}` round-trip | yes | Persisted format |
| …#security_info_rpk_ser_des_then_equal `:56` | RPK → `{"ep","rpk":{"x","y","params":"secp256r1"}}` | yes | |
| …#security_info_oscore_ser_des_then_equal `:82` | OSCORE setting round-trip | yes | |
| …#testOscoreMasterSalt_NullValue `:94` | null master salt → empty array after round-trip | yes | null salt = empty salt |
| …#testOscoreMasterSalt_EmptyArray `:111` | empty salt stays empty | yes | |
| SerializationTests#ensure_SecurityInfo_is_serializable `SRV/SerializationTests.java:28` | Java-serializable | no | |
| SecurityInfoTest#assertEqualsHashcode `SHR/security/SecurityInfoTest.java:24` | equals contract | no | |

_From group D:_

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| RedisPskTest `IT/security/RedisPskTest.java:21` | Reruns all 11 PskTest methods with Redis registration+security store. | yes | Incl. compromised-removal listener and server-initiated handshake lookup via Redis. |
| RedisRpkTest `IT/security/RedisRpkTest.java:21` | Reruns 5 RpkTest methods on Redis. | yes | |
| RedisRpkX509Test `IT/security/RedisRpkX509Test.java:21` | Reruns 2 RpkX509Test methods on Redis. | yes | |
| RedisX509Test `IT/security/RedisX509Test.java:21` | Reruns 35 X509Test methods on Redis. | yes | |
| RedisSecurityStoreTest `IT/security/RedisSecurityStoreTest.java:21` | Reruns 4 SecurityStoreTest methods (PSK id / OSCORE RID uniqueness + cleanup) on Redis. | yes | Uniqueness must hold in shared store. |
| RedisOscoreTest `IT/oscore/RedisOscoreTest.java:21` | Reruns 5 OscoreTest methods on Redis. | yes | |
| ...#security_info_rpk_ser_des_then_equal `:56` | RPK info JSON with hex-encoded key round-trip. | yes | |
| ...#security_info_oscore_ser_des_then_equal `:82` | OSCORE info round-trip. | yes | |
| ...#testOscoreMasterSalt_NullValue `:94` | null master salt -> restored as empty array. | yes | null salt == empty salt. |
| ...#testOscoreMasterSalt_EmptyArray `:111` | empty salt stays empty. | yes | |
| OscoreParametersTest#assertEqualsHashcode `leshan-tl-cf-shared-oscore/src/test/java/org/eclipse/leshan/transport/californium/oscore/cf/OscoreParametersTest.java:24` | equals/hashCode contract. | no | leshan-tl-cf-server-coap-oscore and -bsserver-coap-oscore have no tests. |

### Timeouts

_From group A:_

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| LockStepTest#sync_send_without_acknowleged `IT/lockstep/LockStepTest.java:289` | Read with 3s timeout to a client that never ACKs → null (timeout) within 1.5s, once CoAP retransmissions run out (~1s) | yes | The CoAP exchange lifetime can end before the app-level timeout |
| LockStepTest#sync_send_with_acknowleged_request_without_response `:314` | Client sends an empty ACK and never a separate response → still pending at 1.5s, times out at about 3s (app timeout) | yes | After an ACK the app timeout governs (separate response) |
| LockStepTest#async_send_without_acknowleged `:346` | Async: error callback gets TimeoutException type COAP_TIMEOUT within 1.5s, no response | yes | Two timeout kinds: COAP_TIMEOUT (no ACK) vs RESPONSE_TIMEOUT |
| LockStepTest#async_send_with_acknowleged_request_without_response `:373` | Async + ACK only: no callback at 1.5s, then TimeoutException RESPONSE_TIMEOUT at about 3s | yes | |
| LockStepTest#read_timestamped `:452` | Read /1/0/1 SenML-JSON. Client answers 2.05 with a timestamped SenML record → server exposes the timestamped node | yes | Read responses may carry timestamps (overlaps part B) |
| LockStepTest#observe_timestamped `:488` | Observe 2.05 + Observe:2 with timestamped payload → timestamped node. Then an active Cancel (GET Observe=1) answered 2.05 → also parsed | yes | Active cancel is a real GET with a response body |
| LockStepTest#read_composite_timestamped `:539` | Read-Composite (FETCH) of /1/0/1,/3/0/15 → timestamped nodes | yes | |
| LockStepTest#observe_composite_timestamped `:585` | Observe-Composite → timestamped nodes | yes | |

_From group D:_

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| PskTest#server_initiates_dtls_handshake_timeout `IT/security/PskTest.java:303` | Handshake to dead peer -> TimeoutException(DTLS_HANDSHAKE_TIMEOUT) within request timeout. | yes | Request timeout covers handshake. |
| PskTest#..._then_remove_security_info `:466` / RpkTest `:190` / X509Test `:115` | Client Update times out after server dropped connection. | yes | Server silently drops records for unknown DTLS session (client sees timeout, not alert). |
| OscoreTest#..._server_fails_to_send_request `IT/oscore/OscoreTest.java:135` | Read returns null after 500ms (sync timeout => null response). | yes | Sync API: timeout returns null, not exception. |
| BootstrapHandlerTest#two_bootstrap_at_the_same_time_not_allowed `BS/BootstrapHandlerTest.java:128` | Uses DEFAULT_TIMEOUT = 2min per BS request. | yes | 2min > MAX_TRANSMIT_WAIT (`DefaultBootstrapHandler.java:58-60`). |

### Lifetime expiry

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| InMemoryRegistrationStoreTest#client_registration_sets_time_to_live `SRV/registration/InMemoryRegistrationStoreTest.java:79` | A registration with lt=10000 is alive just after add | yes | alive ⇔ lastUpdate + lt·1000 (+ grace) > now (`SRV/registration/Registration.java:287-315`) |
| (no integration test drives expiry end to end; RegistrationTest uses lt=2s only to force Updates) | — | — | **Gap.** Cleaner behaviour is in the "Implied" list below |

### Multiple transports / endpoints

_From group A:_

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| MultiEndpointsTest#register_then_update_on_different_endpoint `IT/MultiEndpointsTest.java:118` | Register and Update on endpoint A → 2.01/2.04. Update sent to endpoint B → 4.00 BAD_REQUEST | yes | Registration is pinned to the server endpoint URI it registered on (`SRV/security/DefaultAuthorizer.java:65-76`). A declined Update/Deregister gets **4.00**, not 4.03 (`RegistrationHandler.java:149-151,195-197`) |
| …#register_then_deregister_on_different_endpoint `:152` | Deregister sent to endpoint B → 4.00 | yes | Same rule |
| …#register_then_send_on_different_endpoint `:185` | Send (SenML-JSON /3/0/1,/3/0/2) to endpoint B → 4.00. Registration unchanged | yes | `SRV/send/SendHandler.java:88-91` |
| …#register_then_send_on_different_endpoint_with_update_on_send `:195` | Same with updateRegistrationOnSend → 4.00 and the registration is **not** updated | yes | The implicit update is also refused (`SendHandler.java:78-82,138`) |
| …#observe_then_send_notification_on_different_endpoint `:243` | Observe /3/0/15 → 2.05. Notification via A is delivered. Notification via B is ignored (none within 1s). Registration unchanged | yes | Observation key is (endpointUri, token) (`RED/.../RedisRegistrationStoreTest.java:77`, ObservationIdentifier) |
| …#observe_then_send_notification_on_different_endpoint_with_update_on_notification `:254` | Same with updateRegistrationOnNotification → still dropped, no registration update | yes | `SRV/observation/ObservationServiceImpl.java:210-230` |
| RedisMultiEndpointsTest `IT/RedisMultiEndpointsTest.java` | Re-runs all 6 MultiEndpointsTest methods with RedisRegistrationStore | yes | |

_From group C:_

| test | behavior asserted | server-relevant? | edge-case notes |
|---|---|---|---|
| LeshanServerTest#testStartStopStart CF/LeshanServerTest.java:40 | start, stop, start again no exception | yes | restartable server |
| LeshanServerTest#testStartDestroy :57 | after destroy, thread count back to baseline (after forcing presence timer + async request) | yes | no leaked goroutines/timers |
| LeshanServerTest#testStartStopDestroy :79 | same with stop before destroy | yes | |
| LeshanServerTest#testStartStopDestroyQueueModeDisabled :103 | same w/ queue mode disabled (presenceService null) | yes | queue mode optional |
| LeshanServerBuilderTest#create_server_with_default_californiumEndpointsProvider CF/LeshanServerBuilderTest.java:92 | default provider -> 1 COAP endpoint | yes | |
| LeshanServerBuilderTest#create_server_without_securityStore :101 | COAP+COAPS providers but no security store -> only COAP endpoint, store null | yes | COAPS endpoint silently skipped w/o security store |
| LeshanServerBuilderTest#create_server_with_securityStore :113 | with store -> COAP + COAPS endpoints | yes | |
| LeshanServerBuilderTest#create_server_with_coaps_only :127 | COAPS-only provider -> 1 COAPS endpoint | yes | |
| LeshanServerBuilderTest#create_server_without_psk_cipher :139 | ECDHE_ECDSA-only cipher + RPK keys -> COAPS endpoint built | yes | PSK store not required when no PSK cipher |
| CoapRequestBuilderTest#build_read_request CF/request/CoapRequestBuilderTest.java:107 | Read(3,0) -> GET coap://127.0.0.1:12354/3/0, dest = reg peer addr | yes | |
| CoapRequestBuilderTest#build_read_request_with_non_default_object_path :125 | reg links `</lwm2m>;rt="oma.lwm2m"` -> URI /lwm2m/3/0/1 | yes | alternate path prefix from registration |
| CoapRequestBuilderTest#build_read_request_with_root_path :140 | rt=oma.lwm2m on "/" -> /3 (no double slash) | yes | |
| CoapRequestBuilderTest#build_discover_request :155 | Discover -> GET, Accept=40 (link-format) | yes | |
| CoapRequestBuilderTest#build_write_request :174 | Write UPDATE instance -> POST, CF=TLV, payload array of resource TLVs | yes | partial update = POST |
| CoapRequestBuilderTest#build_write_request_replace :198 | Write single resource replace -> PUT | yes | replace = PUT |
| CoapRequestBuilderTest#build_write_attribute_request :213 | WriteAttributes -> PUT /3/0/14?pmin=10&pmax=100 | yes | attrs as query |
| CoapRequestBuilderTest#build_unset_write_attribute_request :234 | unset -> ?pmin&pmax (no value) | yes | |
| CoapRequestBuilderTest#build_execute_request :254 | Execute -> POST /3/0/12 payload "0='params'" | yes | |
| CoapRequestBuilderTest#build_create_request__without_instance_id :273 | Create(12, resources) -> POST /12, TLV resource values | yes | |
| CoapRequestBuilderTest#build_create_request__with_instance_id :296 | Create(12, instance 26) -> POST /12, TLV OBJECT_INSTANCE id 26 | yes | |
| CoapRequestBuilderTest#build_delete_request :321 | Delete -> DELETE /12/0 | yes | |

## Server behaviors implied but non-obvious

### Registration/queue/redis/lockstep

- **Re-register = replace by endpoint name.** A Register for an existing ep atomically replaces the old registration and **removes all its observations**. The server fires `unregistered(old, removedObs, expired=false, newReg)` then `registered(new, old, removedObs)`, only after the 2.01 is sent (`SRV/registration/InMemoryRegistrationStore.java:89-114`, `RegistrationHandler.java:120-135`). Redis does the same under a per-endpoint lock and deletes the old regId index (`RED/RedisRegistrationStore.java:167-207`, `:191`).
- **Registration id is not reused** on re-register. A fresh id comes from the provider (`RegistrationHandler.java:85-86`, RandomStringRegistrationIdProvider). The old `/rd/<oldId>` stops resolving (old regId index removed: `InMemoryRegistrationStore.java:105-107`).
- **Pending downlink requests are cancelled** when a registration ends (deregister, re-register, expiry), and also on Update when the client's address or port changed (`SRV/LeshanServer.java:229-243`). Callers get RequestCanceledException.
- **Events fire after the response is sent** (`SendableResponse` whenSent callback), so listeners see the state after the reply went out.
- **Endpoint name fallback:** a missing `ep` is taken from the secure identity (PSK identity, X509 CN, OSCORE recipient id decoded as UTF-8), else 4.03 "endpoint name missing" (`RegistrationHandler.java:66-73`, `SHR/DefaultServerEndpointNameProvider.java:49-65`).
- **Response code asymmetry on auth failure:** a declined Register → 4.03. A declined Update/Deregister/Send → **4.00** "forbidden"/"not authorized". An unknown regId on Update/Deregister → 4.04 (`RegistrationHandler.java:106-107,143-151,188-197`; `SRV/send/SendHandler.java:81,90`).
- **Endpoint-URI pinning:** every uplink except Register must arrive on the same server endpoint URI (scheme+host+port) the registration was made on (`SRV/security/DefaultAuthorizer.java:65-76`). Notifications are matched by (endpointUri, token), so a notification on another endpoint is dropped.
- **Identity check:** Register/Update/Deregister are checked against the SecurityStore entry for the ep. Other uplinks (Send, etc.) only need the sender identity to equal the registered identity (`DefaultAuthorizer.java:78-121`). updateRegistrationOnSend/OnNotification skip the security-store check (documented hack, `:84-100`) but still enforce the endpoint URI.
- **Validation:** empty or missing object links → 4.00. Bad `lwm2m` version string → 4.00. Well-formed but unsupported version (1.2) → **4.12** (`core/.../RegisterRequest.java:86-94`, `RegistrationHandler.java:80-82`). Binding validated against the version: Q only in 1.0, T/N only in 1.1 or later (`core/.../BindingMode.java:47-63`). `Q=` query param is rejected in 1.0 (`RegisterRequest.java:105-108`). Update binding is validated against the *registered* version (`RegistrationHandler.java:154-155`). An empty `lt=` value → 4.00 (`leshan-tl-cf-server-coap/.../RegisterResource.java` ~:170-175).
- **Update semantics:** absent fields keep their values. Additional attributes are merged key by key. New object links recompute supported objects, instances, root path and CFs (`RegistrationHandler.java:157-167`). The authorizer's custom data replaces the old data only when it is non-null.
- **Object-link parsing:** the root link is the one with `rt="oma.lwm2m"` (or `</>` with ct). A non-`/` root path filters out links outside it. ct may be quoted or unquoted. TLV is added implicitly for 1.0. Resource-level and non-numeric links are ignored. Default object versions depend on the LwM2M version (core version registry).
- **Lifetime:** default 86400s if `lt` is absent (`SRV/registration/Registration.java:51,542`). Alive ⇔ `lastUpdate + lt·1s + grace > now` (`Registration.java:287-315`). Every Update refreshes lastUpdate even with no fields. In-memory cleaner runs every 2s by default with no grace (`InMemoryRegistrationStore.java:70-72,439-460`). Redis cleaner: every 60s, at most 500 per pass, grace 0, via a sorted set keyed on expiry (`RED/RedisRegistrationStore.java:457,768-774,1014-1016`). Expiry fires `unregistered(expired=true)` with the removed observations (`SRV/registration/RegistrationServiceImpl.java:65-69`).
- **Queue mode "awake":** a Q client is awake after Register, Update, any notification, or any successful response to a server request. Each of these restarts the awake timer (default 93s) (`SRV/queue/PresenceStateListener.java:45-84`, `QueueModeLwM2mRequestSender.java:73-77`). It sleeps when the timer fires, on request timeout, or on UnconnectedPeer (no DTLS session). Requests to a sleeping client throw ClientSleepingException and are not queued. Awake/sleep events fire only on state change. Presence tracking stops on unregistered.
- **Timeout classes:** no ACK → COAP_TIMEOUT after retransmissions, which can come before the caller's timeout. ACK then silence → RESPONSE_TIMEOUT at the caller's timeout.
- **Stale-registration sends:** a request (e.g. Observe) targeting a registration already removed must not create an observation. With Cf it fails before sending (SendFailedException). With java-coap any late 2.05+Observe is ignored (`IT/lockstep/LockStepTest.java:429-448`).

### Device-management ops

- Server does not pre-check paths against registration object links: reads/writes/discover/execute/create on absent objects (/50, /9999, /4) are sent and the 4.04 comes from the client (ReadFailedTest.java:109, ExecuteTest.java:139).
- Security object /0 must look non-existent to a DM server for every op: Read/Write/Execute/Create/Delete -> 4.04 (ReadFailedTest.java:145, WriteFailedTest.java:114, ExecuteTest.java:151, CreateFailedTest.java:112, DeleteTest.java:145).
- Local request validation before send, thrown to the caller, nothing on the wire: Create w/o instance id with non-TLV fmt (core/request/CreateRequest.java:278); Create not on object path (:266); Delete not on instance (DeleteRequest.java:76); Discover on resource-instance or root (DiscoverRequest.java:84-86); Execute not on resource / bad args (ExecuteRequest.java:130-146); Write on object path, Update on single resource/resource-instance, TEXT/OPAQUE on non-single or OPAQUE on non-opaque type (WriteRequest.java:420-480); WriteComposite path must be resource/res-instance (WriteCompositeRequest.java:71); WriteAttributes: root path, non-NOTIFICATION class attr, non-writable attr, value-less attr not allowed, path applicability, pmin<=pmax / epmin<=epmax / lt<gt / lt+2st<gt (WriteAttributesRequest.java:66-91, MixedLwM2mAttributeSet.java:90-147).
- Payload encoding uses the registration's object model and runs synchronously at `send`, even for async API; type mismatch -> CodecException, no callback (WriteSingleValueTest.java:349-375).
- Attribute validation is only per-request on server; merged/inherited invalid state is the client's job: client returns 4.00 on WA (WriteAttributeFailedTest.java:158) or 5.00 on Observe when inherited pmin>pmax (WriteAttributeObserveTest.java:708). Server must handle a failed Observe without creating an observation.
- Unsetting an attribute = query param without value (e.g. `?pmax`) (WriteAttributeDiscoverTest.java:186-199). WA merges into existing set at that level.
- Default content formats: Write/Create default TLV (WriteRequest.java:485, CreateRequest.java:276); Read/Observe with null fmt send no Accept (CoapRequestBuilder.java:96,175); Discover always Accept 40 (:108); Execute args ct=0 only if payload (:140).
- Legacy content-format codes 1542 (old TLV) and 1543 (old JSON) must still be encodable for Write/Read (ContentFormat.java:41-42, WriteSingleValueTest.java:90).
- Response Content-Format must equal Accept for every Read/ReadComposite (incl. mixed req/resp SenML JSON/CBOR on FETCH) (ReadCompositeTest.java:78-84).
- Create response: parse Location-Path only when client assigned the id ("2/0"); null when ids given (CreateTest.java:136,171).
- Error responses carry a diagnostic payload the server exposes as error message (ReadSingleValueTest.java:159).
- Passive cancel: once server forgets an observation, it must reply RST to subsequent notifications (CoAP/UDP); on TCP only active cancel exists (WriteAttributeHouseKeepingTest.java:200,256-260).
- A composite write touching several observed resources yields one notification; server must deliver it once and attribute it to the right observation (WriteCompositeTest.java:235-247).
- Notification timing tolerance: tests expect pmin/pmax deliveries within ±20% of the period (WriteAttributeObserveTest.java:150) — server must not drop or rate-limit early/late notifications.
- Write Replace on instance removes omitted optional resources; Update leaves them; resource-level Update merges instances, Replace overwrites (WriteMultiValueTest.java:183,216,380,408) — server must choose PUT vs POST correctly.

### Observe/send/cf-coap unit

- Observation is bound to registration id; `addObservation` throws if reg id unknown (SRVm/registration/InMemoryRegistrationStore.java:229-232).
- New observation with same path (single) or same ordered path list (composite) for same reg replaces old one, old one emits `cancelled` (SRVm/registration/InMemoryRegistrationStore.java:259-265, 272-280; CFm/observation/LwM2mObservationStore.java:73-88). Token collision also replaces (:252-255).
- Re-register (same endpoint) drops all observations of previous registration (SRVm/registration/InMemoryRegistrationStore.java:98-100); deregister/expiry likewise (:209) and cancels ongoing requests (SRVm/LeshanServer.java:240-243). Update that changes address/port cancels ongoing requests of previous reg (SRVm/LeshanServer.java:232-236).
- Observation identifier = (server endpoint URI, token); lookups by token are per server endpoint (CFm/endpoint/CaliforniumServerEndpointsProvider.java:155-157).
- Notification with unknown token or whose sender identity has no registration: dropped with a log, no event (CFm/endpoint/CaliforniumServerEndpointsProvider.java:158-173). NoSec identity = IP:port, so address change => notifications dropped / Send 4.00 "no registration found" (CFm/send/SendResource.java:66-72). DTLS identities (PSK id, RPK, X509 CN) survive IP change.
- java-coap server looks up observation by token only and checks URI path matches observation path (IllegalStateException otherwise) (JCm/observation/CoapNotificationReceiver.java:80-90); registration fetched by observation reg id.
- Optional modes `updateRegistrationOnNotification` / `updateRegistrationOnSend`: on authorized notification/send, registration's peer address is updated (SRVm/observation/ObservationServiceImpl.java:210-243; SRVm/send/SendHandler.java:126-154). Default off; registration otherwise unchanged.
- Error-coded notification (e.g. 4.04 after delete of observed node) is surfaced as a response with that code, and the CoAP observe relation ends -> observation cancelled (IT/observe/ObserveTest.java:217; CFm/endpoint/ServerCoapMessageTranslator.java:104-106).
- Undecodable notification (bad content format/payload) -> `onError(InvalidResponseException)`, observation kept (CFm/endpoint/ServerCoapMessageTranslator.java:147-155; IT/observe/ObserveServerOnlyTest.java:140).
- Composite notification decode is restricted to observed paths; any extra path -> InvalidResponseException (CFm/endpoint/ServerCoapMessageTranslator.java:129-131; IT/observe/ObserveCompositeTimeStampTest.java:376-378).
- Timestamped notifications: if single entry without timestamp -> plain content; else content = most recent, full list exposed; null timestamp allowed alongside real ones (CFm/endpoint/ServerCoapMessageTranslator.java:112-118, 133-141).
- Passive cancel (server-local) removes from store + fires cancelled; subsequent client notification gets RST (client must keep working). Active cancel (GET Observe=1 / FETCH Observe=1 with same token) returns 2.05 with current value but does NOT remove store entry (IT/observe/ObserveCompositeTest.java:449-455).
- Send validation: every path in Send payload must be a registered object (for object paths) or a registered object instance, else 4.04 (SRVm/send/SendHandler.java:169-188). Unsupported content format -> 4.00 "Unsupported content format"; codec error -> 4.00 "Invalid Payload"; both fire send-error listener (CFm/send/SendResource.java:77-86, 112-117). Data listener fires only after response sent (SRVm/send/SendHandler.java:98-105).
- Presence: notifications/new observations feed queue-mode awake state (SRVm/LeshanServer.java:197-201; SRVm/queue/PresenceStateListener.java:72-93).
- COAPS endpoint is only created when a security store is configured (CF/LeshanServerBuilderTest.java:101-123).
- Downlink mapping: Read/Discover/Observe=GET (Discover Accept 40, Observe=0), Write replace=PUT, partial update=POST, WriteAttributes=PUT with query (unset = bare key), Execute=POST text args, Create=POST on object, Delete=DELETE; URIs prefixed by registration alternate path (CF/request/CoapRequestBuilderTest.java:107-354).

### Bootstrap/security

- Endpoint name derivation when `ep` missing: PSK -> PSK identity; X509 -> cert CN; OSCORE -> recipient id as UTF-8 (null if invalid); RPK/NoSec -> none (`DefaultServerEndpointNameProvider.java:49-66`). Missing+underivable: Register -> 4.03 (`RegistrationHandler.java:72`), Bootstrap-Request -> 4.00 (`DefaultBootstrapHandler.java:96`).
- Identity mismatch codes differ by op: Register declined -> 4.03 (`RegistrationHandler.java:107`); Update/Deregister declined -> 4.00 "forbidden" (`:150`, `:196`); Bootstrap unauthorized -> 4.00 (`DefaultBootstrapHandler.java:108`); unknown reg id on Update/Deregister -> 4.04 (`RegistrationHandler.java:144,189`).
- Security check rules (`SecurityChecker.java:86-122`): secure identity with no SecurityInfo for ep -> reject; NoSec peer when SecurityInfo exists for ep -> reject ("must use secured way"); mode must match (PSK info can't be used by RPK client etc.); PSK: identity string equal; RPK: public key equal; X509: CN == endpoint name (not compared to stored cert); OSCORE: recipient id bytes equal.
- Lookup is by claimed ep (`securityStore.getByEndpoint(ep)`, `DefaultAuthorizer.java:106`), so a valid DTLS identity belonging to another ep is rejected at CoAP layer (4.03), not at handshake.
- Update/Deregister are re-checked against security store; Update via Send/Notification ("updateRegistrationOnSend/Notification") skips check (`DefaultAuthorizer.java:89-100`). Other uplinks (Send, etc.) only require same identity as registration (`:113-119`) and same server endpoint URI as registration (`:65-76`) — client can't switch server endpoint (e.g. coap->coaps) within a registration without re-registering.
- Downlink identity binding: server must refuse to send to a peer whose current DTLS identity differs from the registration's identity (EndpointMismatchException) (`IT/security/ServerOnlySecurityTest.java:221-224`).
- Server-initiated DTLS handshake allowed only for non-queue-mode registrations (handshake mode AUTO vs default NONE) (`DefaultCoapsIdentityHandler.java:91-97`, `CoapsServerEndpointFactory.java:85`, `Registration.java:294-297`). Queue mode with no session -> immediate UnconnectedPeer error and client marked sleeping (`IT/security/PskTest.java:384-387`). For server-initiated PSK handshake, PSK identity chosen from registration found by peer address (`LwM2mPskStore.java` getIdentity).
- Security info removal with `compromised=true` must tear down live sessions: DTLS connections by principal (PSK id / RPK key / X509 CN==ep) (`ConnectionCleaner.java:42-78`), TCP/TLS connections (`JavaCoapsTcpServerEndpointsProvider.java:126-135`), OSCORE contexts by RID (`OscoreContextCleaner.java:78-84`). Non-compromised removal does not kill DTLS sessions (`CoapsServerEndpointFactory.java:320`).
- OSCORE context retained on re-registration with same identity, dropped on deregistration/expiry or identity change (`OscoreContextCleaner.java:61-74`).
- Security store uniqueness: PSK identity and OSCORE RID are unique across endpoints; re-adding an ep replaces its info and releases old indexes (`InMemorySecurityStore.java:105-125`; `IT/security/SecurityStoreTest.java`).
- SNI: server selects cert/RPK by client SNI hostname; per-host chain (incl. intermediates) must be configurable (`IT/security/SniTest.java`, `IT/bootstrap/SecureBootstrapTest.java:343`). Server should keep virtual host for downlinks (`DefaultCoapsIdentityHandler.java:89`).
- X509 server should send full chain; missing intermediate breaks clients using service-cert/CA constraints (`IT/security/X509Test.java:826`). DTLS role SERVER_ONLY is tested; server must support both mutual X509 and RPK client vs X509 server (`IT/security/RpkX509Test.java:120`).
- Bootstrap request order: Delete(s) (config order; `/` allowed) -> Write /0/x -> Write /1/x -> Write /2/x -> Write /21/x -> Bootstrap-Finish (`BootstrapUtil.java:217-241`). Writes start only after Bootstrap-Request 2.04 has been sent (`DefaultBootstrapHandler.java:131-134`). One request in flight at a time per session.
- Bootstrap error policy: 4.xx/5.xx on Delete/Write/Discover -> continue with next request; error on Finish -> FINISH_FAILED; transport error/timeout on any request -> REQUEST_FAILED (`DefaultBootstrapSessionManager.java:141-191`, `DefaultBootstrapHandler.java:197-216`).
- No config for ep -> Bootstrap-Request 4.00 "no bootstrap config", session failed NO_BOOTSTRAP_CONFIG (`DefaultBootstrapHandler.java:124-129`).
- Concurrent Bootstrap-Request for same ep: new session accepted, old one CANCELLED and its in-flight request cancelled (`DefaultBootstrapHandler.java:113-120`).
- Content format for BS writes: config value > Bootstrap-Request `pct` > TLV (`BootstrapConfigStoreTaskProvider.java:81-86`, `DefaultBootstrapSession.java:107-111`).
- autoIdForSecurityObject: first Bootstrap-Discover `/`; BS-server instance = `/0/x` link without `ssid`; write BS security to x, renumber DM securities skipping x; Discover failure or no such link -> skip to Finish (`BootstrapConfigStoreTaskProvider.java:52-77,104-113`, `BootstrapUtil.java:243-260`).
- Bootstrap-Request additional query params preserved on session (`IT/bootstrap/BootstrapTest.java:345`).
- Execute /1/x/9 (Bootstrap-Request Trigger) causes client deregister+rebootstrap; DM server should expect the Execute to be cancelled by a racing Deregister (pending requests cancelled on deregister) (`IT/bootstrap/BootstrapTest.java:436-443`).
- BS config validation: certificates in Security object must be DER (PEM rejected) (`BS/DefaultConfigurationCheckerTest.java:43`); cipher-suite ids are 16-bit (`BS/BootstrapConfigTest.java:85`).
- BS server builder: COAPS endpoint only if a security store is configured (`CFBS/LeshanBootstrapServerBuilderTest.java:115-153`).

## Test counts per area

Methods, de-duplicated across groups. Redis* subclasses add no methods; they re-run their parents against the Redis stores.

| area | methods |
|---|---|
| registration | 26 |
| update | 5 |
| deregister | 2 |
| queue mode | 6 |
| read | 12 |
| write | 23 |
| write-attributes | 48 |
| discover | 10 |
| execute | 7 |
| create/delete | 12 |
| observe/cancel | 41 |
| composite ops | 24 (read/write-composite 8 + observe-composite 16) |
| send | 9 |
| block-wise | 0 (none, gap) |
| bootstrap | 47 (incl. 4 OSCORE bootstrap) |
| security PSK | 14 |
| security RPK | 8 |
| security X509 | 39 |
| security OSCORE | 7 |
| redis/clustering | 13 own (12 registration/serialization + 1 OscoreParametersTest), plus 11 Redis* rerun classes |
| timeouts | 8 (LockStep), plus 4 security rows cross-referenced |
| lifetime expiry | 1 (no end-to-end expiry test, gap) |
| multiple transports/endpoints | 27 (MultiEndpoints 6 + cf server lifecycle/request mapping 21) |
| **total** | **389** |

## Per-group count detail

### Group A

Test methods (not parameter expansions). Redis subclasses add no methods but re-run their parent's.

| area | count | breakdown |
|---|---|---|
| registration | 26 | IT RegistrationTest 5 · LockStep 5 · RegistrationHandlerTest 3 · server RegistrationTest 12 · SortObjectLinks 1 |
| update | 5 | InMemoryRegistrationStoreTest 2 · RegistrationUpdateTest 3 |
| deregister | 3 | RegistrationTest#deregister_cancel_multiple_pending_request · LockStep#register_deregister_observe · DeleteClientOnly 1 |
| queue mode | 6 | QueueModeTest 4 · PresenceServiceTest 2 |
| multiple transports/endpoints | 6 | MultiEndpointsTest 6 (+6 re-run by RedisMultiEndpointsTest) |
| timeouts | 8 | LockStep 4 timeout + 4 timestamped-response |
| lifetime expiry | 1 | InMemoryRegistrationStoreTest#client_registration_sets_time_to_live (no e2e expiry test) |
| redis/clustering | 12 | RedisRegistrationStoreTest 3 · RegistrationSerDes 2 · SecurityInfoSerDes 5 · SerializationTests 1 · SecurityInfoTest 1 (+ RedisRegistrationTest re-runs 6) |
| **total** | **67** | LockStepTest has 14 methods (5 reg + 1 dereg + 8 timeouts). IT RegistrationTest has 6 (5 reg + 1 dereg) |

### Group B

| area | test methods |
|---|---|
| read | 12 (ReadSingle 3, ReadMulti 3, ReadOpaque 1, ReadFailed 5) |
| write | 23 (WriteSingle 12, WriteMulti 6, WriteOpaque 2, WriteFailed 3) |
| write-attributes | 48 (Observe 24, HouseKeeping 14, Discover 5, Bootstrap 4, Failed 1 [6 data cases]) |
| discover | 10 |
| execute | 7 |
| create/delete | 12 (Create 4, CreateFailed 3, Delete 4, DeleteClientOnly 1) |
| composite ops | 8 (ReadComposite 4, WriteComposite 4) |
| total | 120 methods |

### Group C

| area | methods |
|---|---|
| observe/cancel | 41 (ObserveTest 11, ObserveServerOnlyTest 1, ObserveTimeStampTest 4, DynamicIPObserveTest 5 [1 disabled], ObservationServiceTest 5, LwM2mObservationStoreTest 4, ObserveUtilTest 4, LwM2mResponseBuilderTest 1, CoapRequestBuilderTest observe 1, + 2 Redis rerun classes) |
| composite ops (observe-composite) | 16 (ObserveCompositeTest 9, ObserveCompositeTimeStampTest 6, LwM2mResponseBuilderTest 1) |
| send | 9 (SendTest 2, SendTimestampedTest 1, LockStepSendTest 1, DynamicIPSendTest 5 [1 disabled]) + 2 Redis rerun classes |
| multiple transports / lifecycle / request mapping | 21 (LeshanServerTest 4, LeshanServerBuilderTest 5, CoapRequestBuilderTest 12 non-observe) |
| block-wise | 0 |
| timeouts | 0 (config only) |
| redis/clustering | 4 rerun classes (RedisObserveTest, RedisDynamicIPObserveTest, RedisSendTest, RedisDynamicISendTest) |

Pure codec/node tests in these modules: none (CoapRequestBuilderTest decodes TLV only to check request shape; DummyDecoder CF/DummyDecoder.java is a helper).

### Group D

Methods (not transport expansions):
- bootstrap: 47 (BootstrapTest 14, SecureBootstrapTest 10, OscoreBootstrapTest 4, BootstrapHandlerTest 4, BootstrapConfigTest 4, DefaultConfigurationCheckerTest 2, InMemoryBootstrapConfigStoreTest 1, LeshanBootstrapServerBuilderTest 5, LeshanBootstrapServerTest 3)
- security PSK: 14 (PskTest 11, ServerOnlySecurityTest 1, SecurityStoreTest PSK 2)
- security RPK: 8 (RpkTest 5, RpkX509Test 2, SniTest RPK 1)
- security X509: 39 (X509Test 35, SniTest X509 4)
- security OSCORE: 7 (OscoreTest 5, SecurityStoreTest OSCORE 2) (+4 OSCORE bootstrap counted under bootstrap)
- redis/clustering (security): 6 subclasses rerunning 62 methods + SecurityInfoSerDesTest 5 + SecurityInfoTest 1 + OscoreParametersTest 1 = 7 own methods
- timeouts: 0 dedicated (4 rows cross-referenced above)
Total own methods in part D: 47+14+8+39+7+7 = 122.

