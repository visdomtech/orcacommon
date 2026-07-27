# Changelog

## [0.3.3](https://github.com/visdomtech/orcacommon/compare/v0.3.2...v0.3.3) (2026-07-27)


### Bug Fixes

* **postgres:** support datapath query param for postgres:embedded: URL ([#8](https://github.com/visdomtech/orcacommon/issues/8)) ([461746f](https://github.com/visdomtech/orcacommon/commit/461746fcb38a55d563a1162261af830bedb81c7f))

## [0.3.2](https://github.com/visdomtech/orcacommon/compare/v0.3.1...v0.3.2) (2026-07-27)


### Bug Fixes

* track embedded Postgres instances and stop them in gracefulShutdown and improve mailgun ([#6](https://github.com/visdomtech/orcacommon/issues/6)) ([511f63a](https://github.com/visdomtech/orcacommon/commit/511f63a6acec9e8a8198bea047b6e9d7bb752cd0))

## [0.3.1](https://github.com/visdomtech/orcacommon/compare/v0.3.0...v0.3.1) (2026-07-27)


### Bug Fixes

* create a new release to fix the go.mod sum ([c9dfc65](https://github.com/visdomtech/orcacommon/commit/c9dfc65114c4cda498a2373c0a9938a6592dce36))

## [0.3.0](https://github.com/visdomtech/orcacommon/compare/v0.2.1...v0.3.0) (2026-07-27)


### Features

* **postgres:** add embedded postgres support ([b488776](https://github.com/visdomtech/orcacommon/commit/b4887763e0a5acaa522f8e453ded4645bf4b0d57))
* **utils:** add GetFreePort helper function ([991a69e](https://github.com/visdomtech/orcacommon/commit/991a69e8c13534d432a6fef10d59cff887d1adf3))


### Bug Fixes

* add shared email package with Mailgun client ([5b04a84](https://github.com/visdomtech/orcacommon/commit/5b04a84e7f4bc2e80e0b4bb3884fa5a805aa7785))
* address review findings for embedded postgres and GetFreePort ([562a085](https://github.com/visdomtech/orcacommon/commit/562a08536ab24ca3af5e14e759098207bdf52ef7))
* merge shared pool into the keyed pools ([6cbe4fa](https://github.com/visdomtech/orcacommon/commit/6cbe4fa76f04ad15f7a13284f4530315d254a561))

## [0.2.1](https://github.com/visdomtech/orcacommon/compare/v0.2.0...v0.2.1) (2026-07-19)


### Bug Fixes

* add RequestHost to return `x-forwarded-host` then `request.Host` ([9fe0f57](https://github.com/visdomtech/orcacommon/commit/9fe0f57e699bd35f68d087b637a8591b7030dbd4))

## [0.2.0](https://github.com/visdomtech/orcacommon/compare/v0.1.3...v0.2.0) (2026-07-13)


### Features

* **postgres:** guard against duplicate migration versions in embedDir ([439ff60](https://github.com/visdomtech/orcacommon/commit/439ff60804ae47b2ce8c966380a1e170b3857287))
