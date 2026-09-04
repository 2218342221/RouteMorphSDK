# Third-party notices

RouteMorphSDK's Go module uses the Go standard library and has no third-party
module dependencies at the time of this notice. The release tree does not
vendor or embed the source of the compatibility reference named below.

## Protocol compatibility research

During protocol compatibility research, behavior and public wire shapes were
cross-checked against:

- **QuantumNous/new-api**, commit
  `2b6f1dfefbe217fed31fc0726717cc7de6958e8e`,
  [source repository](https://github.com/QuantumNous/new-api), licensed under
  the [GNU Affero General Public License version 3](https://github.com/QuantumNous/new-api/blob/main/LICENSE).

`new-api` is not imported, linked or included as a Go module dependency of
RouteMorphSDK. RouteMorphSDK protocol mappings are maintained against official
provider documentation, official SDK type definitions and publicly observable
wire formats.

This notice records compatibility-research provenance. It is not a legal
opinion or a guarantee about copyright, licensing, interoperability or future
provider behavior.

Provider and product names are trademarks of their respective owners. Their use
describes protocol compatibility and does not imply endorsement.

