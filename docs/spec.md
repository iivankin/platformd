# platformd: lightweight single-VPS App Platform

Статус: implementation-ready 2.0
Дата: 12 июля 2026
Целевая среда: одна Linux VPS
Приоритет: простота реализации и эксплуатации

## 1. Назначение

`platformd` — одноузловая App Platform с интерфейсом в духе Railway. Пользователь управляет проектами и сервисами через web-панель, публичный API или MCP. Сервис ссылается на готовый OCI image; при появлении нового digest платформа скачивает image и разворачивает его.

Продукт распространяется одним self-contained файлом `platformd`. Он содержит control plane, HTTPS proxy, admin UI, OCI Registry, private S3-compatible store и приватный container-runtime bundle.

Платформа рассчитана на одну доверенную команду и одну VPS. Она не является Kubernetes, кластерным scheduler или hostile multi-tenant sandbox.

## 2. Зафиксированные ограничения

- Одна VPS и один экземпляр `platformd`.
- Один публичный CLI: `platformd init` с допустимыми bootstrap/reset flags.
- Только готовые OCI/Docker images. Dockerfile build, BuildKit и CI executor отсутствуют.
- Caddy, nginx, Traefik, Docker, containerd и Kubernetes отсутствуют.
- Весь входящий HTTP(S)-трафик приходит через Cloudflare на TCP `443`.
- Cloudflare zone использует `Full (strict)`.
- Firewall провайдера разрешает TCP `443` только из актуальных Cloudflare IP ranges. Platformd не синхронизирует правила firewall провайдера.
- Админ-панель защищена Cloudflare Access; platformd обязательно проверяет Access JWT самостоятельно.
- Одна replica на service в v1.
- Managed PostgreSQL и Redis используют закреплённые официальные OCI images.
- PostgreSQL backup — logical `pg_dump`; Redis backup — RDB. PITR и replication отсутствуют.
- Embedded S3 может иметь public Cloudflare hostname, но anonymous public-read отсутствует: каждый object request требует SigV4 либо presigned URL. ACL отсутствуют.
- Platformd написан на Go. Frontend — React SPA.

## 3. Что означает «один бинарник»

### 3.1. Формат поставки

Выпускается один Linux/amd64 executable `platformd`, одинаковый для всех поддерживаемых hosts. Через `go:embed` в Go executable включены:

- frontend assets;
- migration files и default configuration;

Private runtime payload добавляется release pipeline-ом в конец executable как deterministic self-extracting ZIP:

- `crun`;
- `conmon`;
- `netavark`;
- `catatonit`;
- seccomp profile и containers configuration.

`platformd init` публикует self-contained release slot, в котором binary и соответствующий ему runtime payload всегда находятся вместе:

```text
/var/lib/platformd/releases/<platform-version>/
├── platformd
├── release-manifest.json
└── runtime/
    ├── crun
    ├── conmon
    ├── netavark
    ├── catatonit
    ├── containers.conf
    ├── storage.conf
    ├── registries.conf
    └── seccomp.json
```

ZIP имеет fixed v1 profile, который предыдущий v1 binary может разобрать стандартным ZIP reader без запуска нового executable. Linux игнорирует appended ZIP при запуске ELF, поэтому итоговый файл остаётся обычным executable. Archive содержит canonical `bundle-manifest.json` и только перечисленные им regular entries под `runtime/`; порядок entries lexical, timestamps/extras отсутствуют, compression method только Store либо Deflate. Parser сначала ограниченно читает central directory/manifest, проверяет entry count, compressed/uncompressed aggregate sizes и overflow, отклоняет duplicate/unknown names, encryption, symlink/hardlink/device и любой non-canonical/path-traversal path, затем извлекает files через bounded readers. ZIP64 в v1 запрещён.

Bundle manifest содержит format version, relative path под `runtime/`, file mode, size и SHA-256 каждого helper/config payload; отдельного release/bundle version нет. Весь итоговый файл уже защищён size/hash/signature внешнего release manifest §31. Binary и canonical signed `release-manifest.json` копируются в staging slot, payload извлекается, каждый file сверяется с bundle manifest, весь slot fsync-ится и публикуется одним atomic rename. Helpers не добавляются в host `PATH`; platformd/libpod используют только absolute paths внутри current release slot.

Это single-file distribution, но не single-process runtime. Helpers являются приватной реализацией продукта и не образуют публичный CLI.

Bundle не содержит private glibc, ELF loader или private shared libraries. Platformd и helpers динамически используют только зафиксированные SONAME из базового ABI поддерживаемых Ubuntu/Debian: loader/glibc, libm, GCC unwind runtime, libseccomp, libcap, json-c, GLib, PCRE2 и libatomic. Эти packages не копируются в release slot и обновляются обычными security updates host OS. `platformd init` не запускает package manager: отсутствие либо несовместимость любой required library является failed host probe до установки. Libseccomp обязателен для OCI seccomp policy, а GLib остаётся upstream dependency conmon; их самостоятельные fork/rewrite не вводятся.

Release CI собирает Linux/amd64 artifact один раз против Ubuntu 24.04 baseline, затем проверяет **те же bytes** на чистых Ubuntu 24.04 и Debian 13. На каждом host `readelf`/`ldd` проверяют полный direct/transitive dependency graph против exact SONAME allowlist из build lock; bundled loader/glibc/shared libraries и любой незаявленный SONAME запрещены. Затем CI выполняет реальный create/start/exec/network/remove container cycle. Случайная зависимость от host Podman, GPGME, CNI, hooks, CDI, SELinux/AppArmor files или `/etc/containers` запрещена.

Единственный top-level release/build contract — `build.lock.json`. Нормативная структура:

```json
{
  "formatVersion": 1,
  "toolchain": {
    "go": "exact-version",
    "bun": "exact-version",
    "rust": "exact-version",
    "ccByArchitecture": {
      "amd64": "exact-toolchain-id"
    }
  },
  "goBuild": {
    "cgo": true,
    "tags": [
      "containers_image_openpgp",
      "exclude_graphdriver_btrfs",
      "exclude_graphdriver_zfs",
      "grpcnotrace",
      "seccomp"
    ]
  },
  "helpers": {
    "crun": {
      "revision": "exact-commit",
      "sourceSha256": "sha256:...",
      "buildFlags": ["exact", "canonical", "flags"]
    },
    "conmon": {
      "revision": "exact-commit",
      "sourceSha256": "sha256:...",
      "buildFlags": ["exact", "canonical", "flags"]
    },
    "netavark": {
      "revision": "exact-commit",
      "sourceSha256": "sha256:...",
      "buildFlags": ["exact", "canonical", "flags"]
    },
    "catatonit": {
      "revision": "exact-commit",
      "sourceSha256": "sha256:...",
      "buildFlags": ["exact", "canonical", "flags"]
    },
    "sqlite": {
      "version": "exact-version",
      "sourceSha256": "sha256:...",
      "buildFlags": ["exact", "canonical", "flags"]
    }
  },
  "nativeDependencies": {
    "allowedHostSonamesByArchitecture": {
      "amd64": [
        "ld-linux-x86-64.so.2",
        "libatomic.so.1",
        "libc.so.6",
        "libcap.so.2",
        "libgcc_s.so.1",
        "libglib-2.0.so.0",
        "libjson-c.so.5",
        "libm.so.6",
        "libpcre2-8.so.0",
        "libseccomp.so.2"
      ]
    }
  },
  "targets": [
    {
      "os": "linux",
      "architecture": "amd64",
      "supportedHosts": ["ubuntu-24.04", "debian-13"],
      "minimumSystemd": 255
    }
  ],
  "features": {
    "cgroupV2": true,
    "seccomp": true,
    "overlayfs": true,
    "nftables": true,
    "systemdScopes": false
  },
  "protocols": {
    "ociImage": "exact-revision",
    "ociDistribution": "exact-revision",
    "mcp": "2025-11-25"
  }
}
```

Все fields обязательны, arrays canonical-sorted, unknown fields отклоняются. `goBuild.tags` намеренно оставляет libpod на `cgroupfs`, file events и `k8s-file`: tag `systemd` запрещён, потому что platformd не использует libpod journald/systemd-cgroup integration и не должен получать dependency на libsystemd. AppArmor, libsubid, Btrfs и ZFS graph drivers также не входят в runtime profile; OpenPGP implementation не зависит от host GPGME. `allowedHostSonamesByArchitecture` содержит полный union direct/transitive ELF dependencies всех outputs; CI отклоняет missing либо extra SONAME и выполняет helpers на обоих hosts. Source archive hash и effective canonical build flags обязательны для каждого helper. Exact Go dependencies, включая pinned/forked libpod/containers modules, имеют единственный source of truth в `go.mod`/`go.sum`, а frontend dependencies, включая `ghostty-web`, — в `bun.lock`; они не дублируются в top-level lock. `build.lock.json` фиксирует toolchains, non-Go helpers, build flags, host ABI, supported targets/features и external protocol revisions. Release CI проверяет фактическую сборку против lock, затем генерирует bundle manifest с output hashes и внешний signed release manifest с hash готового platformd. Build lock не хранится в SQLite, не синхронизируется runtime и отдельно не подписывается.

Первый production `init` сам проверяет published release manifest/signature/checksum по §5; отдельный sidecar input operator не нужен. Старый release slot целиком не удаляется, пока он является `current` либо `previous`; binary, manifest и helpers никогда не очищаются независимо друг от друга.

### 3.2. Поддерживаемые hosts

Один и тот же release artifact тестируется минимум на:

- Ubuntu 24.04 LTS, `amd64`;
- Debian 13, `amd64`.

Обязательны systemd, cgroup v2, OverlayFS, nftables-capable kernel и синхронизированные system clock. `init` выполняет active probes; корректные часы являются runtime prerequisite для TLS, Access JWT, SigV4 и release validation. Произвольный Linux ABI не обещается. Bundle не использует host package manager; dependency closure обеспечивается §3.1.

## 4. Runtime architecture

```text
systemd
  └─ platformd
      ├─ embedded libpod + containers/image + containers/storage
      ├─ HTTPS :443 / proxy / UI / API / MCP / Registry / S3
      ├─ project DNS :53/udp+tcp
      ├─ SQLite desired state
      ├─ crun + conmon
      └─ netavark
```

Go-код импортирует exact pinned Podman/libpod source revision и вызывает lifecycle API напрямую. `podman system service`, Podman Unix socket и Podman CLI binary не запускаются и не поставляются. Libpod считается vendored/forkable internal boundary, а не стабильным third-party API: platformd владеет signal handling и shutdown, а несовместимые upstream signal/reexec assumptions patch-ятся в pinned source и покрываются tests.

До первого вызова libpod platformd задаёт полностью private configuration boundary: `CONTAINERS_CONF`, storage/signature/registries policy, persistent graphroot `/var/lib/platformd/containers/storage`, все disposable runroot/libpod/network paths под `/run/platformd/containers`, helper paths и empty hooks/CDI dirs. Читать host `/etc/containers`, default Podman storage/network или global shared-memory locks запрещено contract test.

Libpod adapter является отдельным internal Go package и покрывает pull/image inspect+GC, container list/create/start/wait/stop/kill/remove/inspect, labels, health, logs/attach/exec/resize, network create/inspect/remove и runtime cleanup. Unstable upstream types не выходят за adapter.

Libpod использует cgroupfs manager только внутри delegated `platformd.service` subtree. Unit задаёт `DelegateSubgroup=control`: systemd сразу помещает main platformd process в leaf `control/`, а пустой unit cgroup остаётся inner node, где можно включить cgroup v2 controllers. Platformd создаёт sibling subtree `workloads/`; каждый container payload/catatonit получает собственный leaf под ним, а conmon остаётся в `control/`, чтобы участвовать в graceful shutdown. External systemd scopes отсутствуют.

Нормативное дерево:

```text
platformd.service/
├── control/                 # platformd и conmon
└── workloads/
    ├── services/<deployment-id>/
    ├── resources/<resource-id>/
    └── jobs/<job-id>/
```

До первого container create platformd проверяет delegation, доступность требуемых controllers и фактическое размещение main PID в `control/`, затем включает controllers только на пустых inner nodes. Если contract не выполнен, startup fails до public listeners. Поэтому systemd рекурсивно завершает весь runtime после shutdown deadline. `catatonit` включается как init каждого workload для signal forwarding/zombie reaping.

Platformd регистрирует только exact upstream re-exec handlers, которые нужны выбранной pinned libpod/containers-storage revision. Отдельный generic «internal protocol» не вводится. Conmon остаётся обычным отдельным monitor process. Re-exec handlers не документируются как public CLI и не выполняют product-level administrative actions.

При старте platformd берёт exclusive singleton lock до открытия SQLite/libpod/listeners. Второй daemon отказывается запускаться.

Systemd управляет только `platformd.service`. Platformd создаёт, запускает и удаляет containers через libpod. Каждый daemon process начинает с empty libpod/Netavark actual state и создаёт все desired networks/containers из SQLite; adoption runtime objects предыдущего process отсутствует. Во время жизни одного process reconcile использует ownership labels `io.platformd.*`, inspect-ит созданные им objects и исправляет расхождения.

Libpod DB, network objects, runroot и Netavark config являются disposable actual state в `/run/platformd/containers/`, но containers/storage container records и writable layers физически находятся в persistent graphroot. Поэтому удаление одного `/run` недостаточно. После завершения предыдущего service cgroup каждый daemon startup до public listeners выполняет один normative cleanup:

1. Открывает containers/storage напрямую с temporary cleanup runroot, не создавая libpod Runtime.
2. Перечисляет **все** container records этого private graphroot, unmount-ит и удаляет каждый container/writable layer; foreign containers здесь невозможны по configuration boundary.
3. Проверяет, что container store пуст, но image store сохранён. Любая ошибка удаления останавливает startup; если сам graphroot не открывается/не проходит validation, весь disposable graphroot очищается и создаётся заново.
4. Удаляет platform-owned bridge interfaces и nftables table, полностью пересоздаёт `/run/platformd/containers/` и только затем открывает fresh libpod Runtime.

Adoption отсутствует. Reboot очищает `/run` автоматически, но не отменяет обязательную очистку persistent container records из graphroot.

`containers/storage` graphroot хранится отдельно и после описанной очистки best-effort сохраняется между любыми daemon restarts/self-updates только как non-authoritative image/layer cache. Persistent application/DB/Registry/S3 data всегда находится вне runtime и graphroot. Все networks/containers после каждого restart создаются заново из SQLite, но exact images сначала ищутся в preserved graphroot и pull-ятся только при отсутствии. Abandoned-runtime state и различие ordinary restart/self-update для libpod actual state отсутствуют.

Declared containers/storage compatibility и online graphroot migration отсутствуют. После update новый release просто пытается открыть preserved graphroot. При open/validation error он закрывает storage, полностью удаляет disposable image cache, создаёт empty graphroot и повторно pull-ит exact desired digests. Этот fallback может увеличить downtime и лишить rollback cache, но persistent volumes/DB/Registry/S3 не затрагивает.

## 5. Bootstrap и CLI

Публичная команда:

```text
platformd init [flags]
```

Первый `init`:

1. Проверяет root privileges, host compatibility, свободное место, свободный TCP `443` и cgroup v2. Требование свободного `443` относится только к новой installation; repair существующей installation вместо этого проверяет, что порт обслуживает её systemd unit.
2. По compile-time fixed HTTPS release-manifest URL для собственной version скачивает canonical manifest, проверяет pinned Ed25519 signature и сверяет Linux/amd64, size/SHA-256 текущего executable и bundle checksums. Неподписанный production init запрещён; единственная сохранённая копия manifest размещается в immutable release slot для update/recovery snapshot и не дублируется в SQLite.
3. Создаёт directories и staging initial release slot, копирует туда `platformd` и verified `release-manifest.json`, извлекает private runtime payload и atomically публикует slot.
4. Создаёт SQLite database и, только если файл отсутствует, единственный random 256-bit local master key в `/etc/platformd/master.key`. Существующий path переиспользуется только если это root-owned regular non-symlink file mode `0600` с ровно 32 bytes; любое отклонение останавливает init без перегенерации.
5. Показывает master key в base64url только в interactive root TTY и спрашивает `Ключ сохранён вне VPS? [y/N]`. До ответа `yes` init не продолжает установку и service не запускается. Ответ отдельно не сохраняется; local root всё равно может прочитать файл напрямую.
6. Через interactive prompt/root-only file descriptor принимает в память console passphrase, admin hostname, Cloudflare Origin certificate/key, Access team domain и application AUD; exact issuer `https://<team>.cloudflareaccess.com` и JWKS URL `<issuer>/cdn-cgi/access/certs` выводятся из team domain, а не вводятся отдельно. Secrets и TLS private key через command-line arguments запрещены. Полностью валидирует весь набор, но ещё не публикует partial product rows.
7. Atomically устанавливает `/usr/local/bin/platformd` symlink и один `platformd.service`, выполняет daemon-reload и idempotent enable unit, затем одной SQLite transaction создаёт complete installation configuration со всеми initial settings. Partial installation configuration в SQLite отсутствует.
8. Запускает daemon и выполняет direct local HTTPS health check на `127.0.0.1:443` с admin SNI/Host. Проверка не использует public DNS или system trust store: она требует exact configured leaf certificate, проверяет его hostname coverage и тем самым подтверждает TLS route/key именно этой installation.

Отдельных bootstrap marker, phase и confirmation state нет. Если `init` прерван до появления complete installation configuration, повторный запуск переиспользует существующие master key/SQLite и может снова показать тот же key и задать вопрос независимо от предыдущего ответа. Staging artifacts можно безопасно перезаписать; точное продолжение с последнего шага не обещается. Существующий key никогда автоматически не перегенерируется.

Если complete installation configuration уже существует, обычный повторный `init` не спрашивает initial inputs и не меняет product state, но idempotently восстанавливает/проверяет current symlink и unit, выполняет daemon-reload, enable/start и тот же direct local HTTPS health check. Занятый собственным running unit порт `443` при этом является нормой; другой listener — ошибка. Это закрывает crash после configuration commit, но до шага 8, без bootstrap marker. Все остальные изменения initialized installation разрешены только через явно названные recovery flags либо UI/API.

После successful init обязательного first-run wizard в web UI нет: admin входит через уже настроенный Cloudflare Access и попадает в пустую рабочую панель. Projects, services, дополнительные Origin certificates, API/MCP hostname, Registry/S3 и remote-backup target настраиваются обычными разделами UI. Cloudflare DNS, Origin/Access application и provider firewall operator подготавливает вне platformd до `init`.

Разрешён special bootstrap action той же команды:

```text
platformd init --reset-console-passphrase
platformd init --rollback-update
platformd init --restore
platformd init --install-signed-update <manifest.json> [--binary <platformd>]
```

Все recovery/bootstrap actions требуют local root и не доступны через public API/MCP. Reset passphrase не меняет остальную конфигурацию; rollback работает только при остановленном service по §31. `--restore` применяется только на новой/пустой installation: через interactive prompt/root-only file descriptor принимает сохранённый master key и remote S3 target/credentials, восстанавливает latest complete control generation до запуска daemon и устанавливает сохранённый exact platformd binary/SQLite. По explicit prompt operator может заменить только restored Cloudflare Access team domain/AUD; issuer/JWKS URL снова выводятся из team domain. Origin certificate/hostname и console passphrase по умолчанию приходят из snapshot. Затем запускается admin UI в recovery mode §25.1.

`--install-signed-update` является только local forward-recovery path §31 и не открывает/migrate-ит SQLite. При остановленном unit он сначала проверяет signed manifest current slot, затем local canonical target manifest, pinned Ed25519 signature, target SemVer strictly newer current, Linux/amd64, membership current version в `supportedFrom`, binary size/hash и bundle checksums. Binary берётся из explicit root-owned regular non-symlink `--binary` file либо скачивается по manifest HTTPS URL. Затем command fsync/rename публикует release slot, atomically переключает `current` и запускает unit. Его можно вызвать напрямую из сохранённого previous binary, даже если current daemon не стартует. Все остальные operations выполняются через UI/API/MCP. systemd использует скрытый daemon mode.

## 6. Filesystem layout

```text
/usr/local/bin/platformd

/etc/platformd/
└── master.key                     # 0600; единственный mutable product secret вне SQLite

/var/lib/platformd/
├── state/platformd.db
├── releases/
│   ├── current -> <version>/
│   ├── previous -> <version>/      # до readiness target release; rollback только до migration commit
│   └── <version>/
│       ├── platformd
│       ├── release-manifest.json    # canonical signed external manifest этого binary
│       └── runtime/                # crun/conmon/netavark/catatonit/configs этого release
├── containers/
│   └── storage/                    # preserved non-authoritative containers/storage image cache
├── volumes/<project-id>/<volume-id>/
├── logs/
│   ├── deployments/<deployment-id>/<attempt-id>.log
│   ├── resources/<resource-id>/<attempt-id>.log
│   └── jobs/<backup-or-operation-id>/<attempt-id>.log
├── registry/<repository-id>/
│   ├── blobs/sha256/
│   └── uploads/
├── objects/<store-id>/
│   ├── payloads/
│   └── multipart/
├── backups/work/
└── tmp/

/run/platformd/
├── containers/                     # recreated every daemon start
│   ├── libpod/
│   ├── networks/
│   └── runroot/
├── generated/                      # recreated engine configs/secrets; root-only host directory
├── pty/
└── locks/
```

SQLite работает в WAL mode. SQLite является authoritative product state, включая complete installation configuration, Access settings, encrypted Origin private key, certificate, console-passphrase verifier, secrets, Registry metadata и S3 metadata. JSON применяется только как API representation/export, но platformd не наблюдает config files на диске.

Platform volumes являются platform-managed bind directories из показанного `volumes/` root, а не libpod named/anonymous volumes. Ordinary Volume принадлежит ровно одному Service и не может одновременно подключаться к другому Service. Он хранит immutable numeric `ownerUid`/`ownerGid`. UI автоматически предлагает image owner только когда OCI `User` имеет exact numeric form `<uid>:<gid>`; single UID либо symbolic user/group не считается достаточным для вывода GID и получает explicit `0:0` default с warning и редактируемыми numeric fields до создания. Empty directory создаётся mode `0700` и один раз получает этот owner. После появления данных owner через product API не меняется; platformd никогда не делает recursive `chown` во время deploy. При смене image UID пользователь исправляет ownership осознанно через container/server console либо переносит данные в новый Volume.

Все application volumes монтируются read-write; отдельного read-only mount mode в v1 нет. User-configured application mounts могут ссылаться только на Volume своего Service; arbitrary host paths запрещены. Internal generated engine-config mounts из root-owned `/run/platformd/generated/` не являются application volume API. Delete Volume отклоняется, пока он referenced mutable Service config либо active Deployment; historical Deployment может остаться с missing Volume, и его rollback возвращает `dependency_missing` до изменения Service. Managed PostgreSQL/Redis volumes не являются ordinary Volume: required directory ownership устанавливает engine initialization job выбранного official image до публикации resource. Conmon log path всегда задаётся явно по stable product owner: Deployment для application container, managed resource для PostgreSQL/Redis либо Backup/observational Operation ID для short-lived job. Random `attempt-id` разделяет повторные runtime containers и не сохраняется как authoritative SQLite field; libpod container ID в filesystem path не используется.

Release pin-ит exact SQLite 3.53.2 в `build.lock.json`; формулировки «минимум» и плавающий branch запрещены. SQLite `PRAGMA user_version` является schema version: каждая release migration меняет schema и `user_version` в одной transaction, а binary знает единственное ожидаемое значение. Все mutations проходят через один bounded writer task. S3/Registry payload I/O и hashing выполняются вне SQLite transaction; ordinary metadata commit короткий. Server задаёт busy timeout, ограничивает writer queue и duration обычных transactions и выполняет controlled WAL checkpoints. Единственное явное исключение по duration — atomic replacement metadata очень большого ObjectStore restore §22; он использует тот же единственный writer и не выполняется параллельно с другой mutation. UI/API/MCP используют одни server-side invariants.

Libpod runtime DB/locks существуют только в recreated `/run/platformd/containers` и никогда не читаются следующим daemon process. Platformd не восстанавливает product configuration или actual objects из libpod DB: после каждого ordinary restart, crash, reboot, full restore и self-update containers/networks создаются заново из SQLite. Preserved containers/storage является только cache: наличие image не означает desired resource, а отсутствие вызывает pull exact saved digest.

Normal Registry/S3 payload write использует один protocol внутри exact repository/store directory: write/hash temporary file → `fsync` → atomic rename в immutable payload path → `fsync` parent. S3 затем короткой SQLite transaction публикует object metadata; metadata никогда не ссылается на непроверенный temporary file. Repository-local Registry blob не имеет metadata row и становится доступен по digest после rename; последующий Manifest PUT отдельно проверяет его existence и публикует manifest/tag в SQLite. Crash может оставить только unreferenced durable payload, который owner-scoped startup/cleanup удалит после grace period. Строгая атомарность filesystem+SQLite не обещается. Cross-repository и cross-store payload references отсутствуют.

`backups/work/` содержит только transient manifests/stream buffers текущего process и не является resume state. После daemon startup все прежние files в этом directory удаляются до запуска backup worker; running Backup/Operation records всё равно переводятся в interrupted по §7.

## 7. Control plane и состояние

Каждый класс данных имеет одного владельца:

| Данные | Владелец |
|---|---|
| Projects, services, domain bindings, secrets и credentials | platformd SQLite |
| Registry tags/manifests/policies и S3 store/object metadata | platformd SQLite |
| Registry/S3 payloads, volumes и container logs | filesystem payload stores |
| Container/image/network actual state | libpod/containers-storage/Netavark |
| Internal DNS records | in-memory view из SQLite desired state + libpod inspect |
| Remote backup catalog | self-describing manifests в remote S3 |
| Installed binary/release manifest/runtime config | immutable current release slot |

Двусторонней синхронизации нет. Поток управления только `SQLite desired state → reconcile → libpod actual state`. IP, PID, mount state и runtime status не являются authoritative SQLite fields и получаются через inspect. Remote backup catalog не кэшируется в SQLite: generation list для UI/restore каждый раз получается bounded paginated `LIST` exact resource/control prefix и читается из self-describing remote manifests.

Основные сущности:

- Project;
- Service;
- Deployment;
- Volume;
- Secret;
- RegistryRepository и RegistryCredential;
- ObjectStore и S3Credential;
- ManagedPostgres;
- ManagedRedis;
- Backup;
- ApiToken;
- Operation;
- AuditEvent.

Application domain не является самостоятельной entity: internal `ServiceDomain(hostname primary key, serviceId)` существует только как child binding Service, без synthetic ID, dangling state и отдельного CRUD lifecycle.

Каждая administrative product mutation выполняется SQLite transaction и добавляет audit event. Высокочастотные protocol data operations — S3 PUT/DELETE/multipart parts, Registry blob chunks/pulls и log writes — не создают AuditEvent; их metadata всё равно commit-ится по правилам соответствующего resource. Долгое действие, у которого нет собственного Deployment/Backup status record, возвращает `operationId` для одинакового polling через UI/REST/MCP.

`Operation` является строго observational SQLite record и содержит только `id`, `kind`, `targetId`, status `running/succeeded/failed/interrupted`, короткий progress/error и start/finish timestamps. Он создаётся, когда действие реально началось; durable queue и статус `queued` отсутствуют. Operation не владеет job, lock, candidate objects или publication pointer, не содержит operation-specific resume payload и никогда не используется для решения, что запускать/удалять после crash.

При daemon startup все оставшиеся `running` Operations и Backup records одной transaction становятся `interrupted` до обычного reconcile; scheduled occurrence с уже существующим interrupted Backup автоматически не replay-ится. Неактивный Deployment со status `running` также становится `interrupted`; pointer switch и successful Deployment status обязаны commit-иться одной transaction, поэтому referenced `activeDeploymentId` не остаётся ambiguous. Эти records не resume-ятся и могут показывать `interrupted`, даже если внешний filesystem side effect успел произойти непосредственно перед crash; клиент обязан перечитать target resource. Backup generation, active pointer, filesystem и audit history остаются отдельными domain sources. Так observational progress не становится вторым authoritative state.

Per-resource mutex/optimistic row-version check сериализует deploy/update/delete одного resource. Дополнительно существуют только два in-memory installation-wide gates: `mutationGate` для self-update и `backupTargetGate` для create/replace/delete единственного remote target. Global hostname uniqueness обеспечивается SQLite primary/unique constraints и короткой transaction, отдельного hostname lock/state нет. Единственные durable publication pointers — logical `activeDeploymentId` и managed DB `(imageTag,imageDigest,volumeId)`. ObjectStore и Registry публикуют current metadata обычными SQLite rows без generation pointer. Service хранит mutable desired config, а каждый Deployment — immutable полный snapshot deploy-relevant Service config и exact image digest; отдельная config-revision entity и `activeRevisionId` отсутствуют. Libpod container ID/IP inspect-only; actual container текущего process находится по `deploymentId` ownership label. После daemon crash old runtime целиком уничтожается и новый process создаёт только objects, referenced logical pointers; candidate/orphan adoption отсутствует.

Reconciliation должен быть idempotent. После crash platformd не доверяет сохранённому `running`: observational Operations становятся interrupted, затем clean runtime сходится к SQLite desired pointers. Внутри текущего daemon lifetime correctness основана на inspect, а libpod lifecycle events не образуют отдельный product event store. В SQLite сохраняются только product/audit events, необходимые UI.

## 8. Projects, networks и internal DNS

На каждый project создаётся отдельная IPv4-only rootful Podman bridge network через Netavark с network DNS disabled, IPv6 explicitly disabled и bridge option `isolate=true`. Containers разных projects не подключаются к одной project network. Platformd не хранит назначенный subnet/gateway как второй authoritative state: на каждом startup он сортирует Projects по stable ID, сканирует current host routes/interfaces и deterministic выбирает свободный `/24` из compile-time private candidate pools, затем получает actual parameters через libpod/Netavark inspect. Collision либо исчерпание pools останавливает только affected project workloads с visible error; admin control plane остаётся доступен.

Platformd реализует небольшой DNS server и для каждой project network слушает UDP и TCP `53` на actual bridge gateway IP. Каждый project container получает этот IP как единственный resolver. Service получает записи:

```text
<service>
<service>.<project>.internal
```

Например, `api.shop.internal`. Managed resources получают аналогичные имена: `postgres.shop.internal`, `redis.shop.internal`, `assets.shop.internal`.

Service, managed database и object-store names делят единый project DNS namespace. Имена являются DNS labels и уникальны внутри project; collision между разными resource types отклоняется. Несколько stores/databases получают собственные `<resource>.<project>.internal` names.

Service/managed records указывают только на ready current deployment IP, полученный через inspect. S3 record указывает на project bridge gateway IP. Изменение current deployment заменяет in-memory record; persistent DNS zone files отсутствуют.

Неизвестные public names пересылаются configured host upstream resolvers через bounded cache. Startup отклоняет upstream address, совпадающий с любым platform DNS/S3 listener либо project gateway, чтобы исключить forwarding loop. `.internal` никогда не форвардится наружу. Internal TTL default — 5 seconds. UDP size, TCP connections, recursion depth, cache entries и concurrent upstream requests ограничены.

Listener destination/gateway однозначно определяет project view. Запрос не может получить records другого project. Platformd запускает DNS listener до project containers. При daemon restart workloads останавливаются по §30; DNS и service traffic недоступны до их reconcile/readiness, а survival уже установленных соединений не обещается.

Bind к разным local gateway IP сам по себе не является isolation boundary из-за Linux weak-host model. Platformd владеет одной private nftables table `inet platformd` с drop-only base chains на hooks `input` и `forward` с fixed priority `-200`; later host/Netavark accept rules не могут отменить уже вынесенный drop verdict. Для каждого project разрешаются только пары `(incoming project bridge, destination gateway, protocol, port)` для DNS `53` и принадлежащих проекту internal S3 listeners; обращения с host/public/других project interfaces и forwarding между project bridges отклоняются. Listener и atomically replaced rules публикуются до запуска containers и удаляются после их остановки. Rules пересоздаются из inspected actual networks и не являются authoritative state. External deletion/reload таблицы обнаруживается reconcile и восстанавливается. Exact locked Netavark release обязан проходить packet-level tests совместно с этими priorities.

Внутри проекта east-west traffic разрешён. Между projects маршрутизация отсутствует. Произвольное подключение service к чужой project network запрещено API validation.

## 9. Services

Service содержит:

- project и name;
- OCI image reference с registry credential reference;
- command/args override;
- environment и secret references;
- optional HTTP target port и domains; domain требует target port;
- optional HTTP health path и startup timeout;
- optional CPU и memory hard limits;
- zero или больше named volumes; все они writable;
- `enabled`, default `true`.

Все services являются long-running processes. User-defined cron/one-shot modes отсутствуют. Отдельных watch/restart/mode settings нет: tag reference автоматически poll-ится по §10, digest reference pinned и не poll-ится, а любой enabled service после unexpected process exit перезапускается platformd с fixed bounded in-memory crash-loop backoff. Во время остановленного control plane restart guarantee отсутствует.

Platformd поддерживает OCI/Docker manifests для host architecture. Tag сохраняется в Service как desired reference, но каждый Deployment получает resolved exact digest и всегда запускает digest, а не повторно mutable tag. Digest reference сразу используется как exact digest.

Runtime-affecting изменение enabled Service — image reference/credential, command/args, environment/secrets, target port, health settings, limits или volumes — создаёт новый Deployment с текущим resolved digest. Domain binding меняет только routing и не пересоздаёт container. Mutation `enabled=false` одной SQLite transaction очищает `activeDeploymentId` и live publication; reconcile останавливает runtime. Service, domain bindings, volumes и Deployment history сохраняются. При повторном enable platformd заново resolve-ит tag либо использует pinned digest и создаёт новый Deployment.

Secret value и RegistryCredential authentication material immutable. Rotation всегда создаёт новый internal ID, одной Service mutation заменяет reference в mutable desired config и при enabled Service создаёт новый Deployment. `serviceConfigHash` включает immutable reference IDs. Значения шифруются master key, показываются только при создании и не возвращаются позднее. Delete отклоняется, пока ID referenced текущим Service config либо active Deployment; historical Deployment может сохранить уже удалённый reference, и его rollback тогда явно возвращает `dependency_missing`, никогда не подставляя более новое значение. Platformd намеренно не включает secret values в свои logs, config diff или terminal audit metadata; application может сама напечатать полученный secret в stdout, и надёжная автоматическая redaction этого не обещается.

При запуске referenced secret неизбежно существует plaintext в process environment либо root-only temporary file и может присутствовать в private libpod runtime config. Это принимается в trusted-team model; runtime dir имеет mode `0700`, не backup-ится и удаляется при rebuild. Platformd сам не пишет secret values в audit/error logs.

Для service существует только optional HTTP health check. Если задан `healthPath`, platformd делает GET прямо на container IP/target port с fixed probe interval/timeout, не следует redirects и считает сам ответ `2xx/3xx` ready до configurable `startupTimeout` (default 60 seconds). TCP/exec modes, thresholds и настраиваемые интервалы отсутствуют. Без `healthPath` process считается ready, если не завершился за fixed 3-second startup grace. PostgreSQL/Redis используют отдельные fixed engine checks.

OCI image-declared `VOLUME` не создаёт anonymous persistent volumes: image volume mode фиксирован как ignore. Persistent storage существует только через явно созданные platform volumes.

Host network, privileged containers, host PID namespace, arbitrary host bind mounts, devices и runtime socket не поддерживаются.

## 10. Image watching

Platformd использует только manifest polling для Services с tag reference: default 60 seconds для remote registry и 10 seconds для embedded registry. Service с digest reference не poll-ится. Local wake-up events/outbox отсутствуют. Conditional requests и bounded exponential backoff применяются к registry errors.

Перед registry request platformd вычисляет deterministic hash текущего deploy-relevant Service config. Это derived in-memory value, а не SQLite field. После ответа Service перечитывается; если hash либо image reference изменились, stale result отбрасывается. Resolved digest сравнивается с digest active Deployment, а текущий config hash — с hash его immutable snapshot. При различии создаётся Deployment с полным текущим config snapshot и resolved digest.

Обычный Service reconcile выполняет ту же проверку для config changes и pinned digest references; watcher только получает новый digest для tag reference. Registry resolution/pull failure автоматически retry-ится с bounded exponential backoff, потому что active container при этом не останавливается. Candidate, который был создан, но завершился либо не прошёл readiness, делает exact pair `(serviceConfigHash, digest)` blocked для automatic retry.

Отдельного retry-state нет: latest failed Deployment с тем же immutable config hash/digest уже является durable причиной не повторять pair и переживает daemon restart. Новый digest, изменение deploy-relevant config либо explicit `Redeploy` создаёт новую попытку; ручной `Redeploy` может сознательно повторить ту же pair. Текущий healthy container продолжает работать, пока новый image pull не завершён; затем начинается stop-first downtime §11.

Platformd не строит Dockerfile и не клонирует Git repositories.

## 11. Deployment

В v1 service имеет ровно одну desired replica.

Deployment immutable хранит exact image digest и полный snapshot настроек, необходимых для запуска: image source reference и registry credential reference, command/args, environment и secret references, target port, health settings, CPU/memory limits и volume mounts. `enabled`, domain bindings, Service identity и `activeDeploymentId` остаются Service-level state и в snapshot не входят. Runtime container всегда создаётся из snapshot конкретного Deployment, поэтому более новый mutable Service config не меняет уже active либо failed Deployment задним числом.

Все deployments используют один stop-first workflow независимо от volumes. Initial deployment/re-enable следует тем же шагам с отсутствующим old container и `activeDeploymentId=null`:

1. Pull exact digest, пока old container ещё работает.
2. Пока old container работает, проверить existence всех immutable dependencies и create stopped candidate с новым unique name и настройками из Deployment snapshot. Create/validation failure не затрагивает old runtime.
3. Если old существует, оставить `activeDeploymentId` старым и остановить, но пока не удалять old container. Proxy временно возвращает unavailable, а internal DNS не публикует stopped deployment. Для initial deployment этот шаг отсутствует.
4. Start candidate и дождаться simplified readiness §9.
5. При success одной SQLite transaction переключить `activeDeploymentId`; proxy/DNS начинают использовать candidate, после чего old container, если он был, удаляется.
6. При failure candidate удаляется, а old container, если он был, best-effort запускается снова; pointer не меняется.

Old и candidate никогда не работают одновременно. Каждый deployment имеет downtime от остановки old до readiness candidate либо возврата old; start-before-stop, memory-headroom calculation, DNS drain и rollback window отсутствуют. Cached DNS clients могут кратко обращаться к stopped old IP и должны retry.

Для service с volume возврат old process не откатывает данные: candidate уже мог изменить shared volume несовместимым образом. Volume snapshot/data rollback отсутствует; UI показывает этот риск до Apply. Crash до pointer switch приводит reconcile к cleanup candidate/restart old; crash после switch — к candidate. Строгая 100% durable deployment state machine не обещается.

Rollback не переключает pointer на старый Deployment и не пытается воскресить его container. Сначала platformd проверяет existence всех referenced secrets, credential и volumes; missing dependency возвращает `dependency_missing` без изменения Service. Затем platformd копирует deploy-relevant snapshot выбранного successful Deployment в mutable Service, заменяет image reference exact digest этого Deployment и создаёт новый Deployment. Поэтому rollback детерминирован и автоматически pin-ит image; чтобы снова получать tag updates, operator должен явно выбрать tag reference. Domain bindings и `enabled` rollback не меняет. Риск несовместимых изменений writable volumes остаётся тем же.

Horizontal scaling, multi-replica rolling deployment, autoscaling и cross-host scheduling являются non-goals v1.

## 12. HTTPS ingress и proxy

Platformd слушает TCP `443`, завершает TLS и выбирает route по exact SNI + normalized Host. Используется Cloudflare Origin CA certificate либо другой certificate, принимаемый `Full (strict)`.

Public hostnames делятся на:

- admin UI/API hostname;
- optional automation API hostname;
- optional registry hostname;
- optional S3 hostname per object store;
- application hostnames.

Один hostname не может иметь две роли. Public S3 hostname разрешён и остаётся authorization-private по §21.

Application hostname управляется только в `Service → Settings → Domains`. Attach создаёт `ServiceDomain` сразу с target Service; unattached route отсутствует. Если hostname уже принадлежит другому Service, UI показывает source/target и требует explicit `Move`; одна SQLite transaction меняет `serviceId`, после commit proxy atomically публикует новый routing snapshot без промежуточного unrouted state. Caller обязан иметь admin access одновременно к source и target projects; project-bound token не может забрать domain из чужого project. Detach удаляет child binding. Certificate ID в binding не хранится: platformd автоматически выбирает подходящий exact/wildcard Origin certificate по SNI и отклоняет attach/move без SAN coverage.

Application proxy поддерживает HTTP/1.1, HTTP/2 от Cloudflare, WebSocket и streaming request/response. Proxy добавляет standard forwarding headers, но сначала удаляет incoming spoofable forwarding headers. `CF-Connecting-IP` считается client IP только после прохождения provider firewall assumption.

Platformd не управляет Cloudflare DNS, firewall или Access policies и не содержит certificate automation. UI хранит один или несколько encrypted Origin certificate chains/keys и до создания hostname проверяет SAN coverage; непокрытый route отклоняется. Wildcard покрывает только один left-most DNS label и не покрывает apex. При нескольких подходящих certificates exact SAN имеет приоритет над wildcard, затем выбирается lowest stable certificate ID; порядок не зависит от map iteration. Delete/replace certificate одной transaction разрешён только если после mutation каждый existing public hostname всё ещё имеет coverage; иначе mutation отклоняется с перечнем зависимых hostnames. После commit выполняется atomic TLS config reload. Automatic expiry monitoring/renewal отсутствует.

Unknown SNI/Host, SNI/Host mismatch, duplicate/invalid Host и malformed IDNA отклоняются без default backend. Proxy имеет server-side limits для header bytes/count, connections, slowloris/read/write/idle/upstream timeouts и WebSocket lifetime; request body ordinary application route stream-ится без platform-specific byte cap, но с bounded buffers/concurrency.

## 13. Cloudflare Access

Admin hostname требует `Cf-Access-Jwt-Assertion` на каждом request. Platformd:

- строит exact issuer `https://<team>.cloudflareaccess.com` и JWKS URL `<issuer>/cdn-cgi/access/certs` только из validated configured team domain; HTTPS redirect на другой scheme/host запрещён;
- загружает JWKS только с этого URL и кэширует successful response fixed 6 hours;
- выполняет outbound JWKS refresh через один process-wide singleflight и не чаще одного раза за fixed 30 seconds;
- unknown `kid` сначала проверяет в bounded LRU negative cache (maximum 256 entries, TTL 30 seconds). Если refresh cooldown истёк, выполняет один singleflight refresh; отсутствующий после него `kid` помещается в negative cache. Во время cooldown новый unknown `kid` отклоняется без outbound request;
- при refresh failure отклоняет request с unknown `kid`, а известный cached key использует только до конца текущего 6-hour TTL;
- принимает только `alg=RS256`, проверяет signature, `kid`, exact derived issuer, expiry/not-before с fixed 60-second clock skew и требует, чтобы string либо array claim `aud` содержал exact configured audience;
- требует stable subject и email;
- не доверяет одному наличию Cloudflare headers;
- отклоняет запрос при неизвестном key после описанного bounded refresh flow.

Cloudflare Access является user directory. Local passwords/users отсутствуют. В v1 любой interactive user, пропущенный Access policy для admin application, является administrator. Все mutations и terminal sessions записывают Access subject/email в audit event.

Browser mutations требуют same-origin checks и CSRF protection. Admin HTML/API responses используют `Cache-Control: private, no-store`.

Каждый admin WebSocket handshake (logs и terminals) принимает только exact admin `Origin`, заново валидирует Access JWT до upgrade и не полагается только на Access cookie. Application WebSockets этим admin rule не ограничиваются.

## 14. Admin UI

Frontend stack фиксирован:

- React + TypeScript;
- shadcn/ui как source components;
- Tailwind CSS 4;
- `tailwindcss-bun-plugin`;
- direct `Bun.build`, без Vite/webpack;
- exact versions в `bun.lock`.

Production assets content-hash-ятся и compile-time встраиваются в Go binary. Bun/Node отсутствуют на VPS.

Admin origin использует строгий CSP без `unsafe-inline`/`unsafe-eval`, output escaping и Trusted Types where supported. Inline preview разрешён только для bounded plain text и decoded raster images. HTML, SVG, PDF и любой другой active/binary content никогда не исполняется или render-ится в admin origin и доступен только как download. Secrets не помещаются в DOM attributes, URLs или browser storage.

Attacker-controlled download всегда получает `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff` и safe generic content type. Preview iframe/origin отсутствует; admin CSP включает `object-src 'none'`, `base-uri 'none'` и `frame-ancestors 'none'`.

Интерфейс визуально и по information architecture ориентируется на Railway, но не копирует branding/assets. Основные разделы:

- Overview;
- Projects;
- Service / Deployments / Variables / Volumes / Settings;
- PostgreSQL;
- Redis;
- Object Storage;
- Registry;
- Logs;
- Backups;
- API Tokens;
- Infrastructure;
- Audit.

UI показывает operation progress и фактическое runtime state, а не только desired state.

Backups view является сводным индексом, но enable/UTC cron/retention, `Backup now`, generation list и restore редактируются у конкретного PostgreSQL, Redis, Registry или ObjectStore. Общего platform backup schedule/restore generation нет.

## 15. Logs

Conmon пишет stdout/stderr каждого container в приватный structured log file. Platformd:

- stream-ит новые записи в UI через WebSocket;
- поддерживает service/deployment filters;
- хранит timestamp, stream и text;
- ограничивает размер segment и общий disk budget;
- удаляет старые segments по retention, default 7 days;
- не блокирует workload при медленном browser client.

Для container logs используется только conmon `k8s-file` с pinned revision, поддерживающей native `--log-size-max`, `--log-rotate` и `--log-max-files`. Conmon единолично закрывает, переименовывает и переоткрывает active attempt file при достижении fixed segment size; platformd никогда не rename/truncate-ит открытый conmon file descriptor. Platformd удаляет только закрытые rotated segments и полностью закрытые attempt logs по 7-day retention/global disk budget. Собственный второй rotation protocol отсутствует.

Log ownership основан только на product IDs. Application attempts группируются под Deployment ID, PostgreSQL/Redis containers — под managed resource ID, short-lived jobs — под их Backup либо observational Operation ID. Пересоздание runtime container создаёт новый random attempt segment под тем же owner; container ID остаётся inspect-only и для поиска старых logs не нужен.

Одна log record имеет hard byte limit; oversized record получает truncation marker. Invalid UTF-8 заменяется безопасно, NUL/control sequences не интерпретируются UI как terminal/HTML. Download имеет server-side byte/time cap.

Full-text search engine и внешний log cluster отсутствуют. Предусмотрены simple contains filter по загруженному bounded окну и download выбранного временного диапазона.

Собственные platformd logs пишутся только в journald и доступны в отдельном Infrastructure view; отдельного platformd structured sink/rotation нет.

## 16. Terminals

Обе interactive consoles используют один frontend terminal renderer — exact `ghostty-web`, закреплённый в `bun.lock`, с однократным async WASM `init()`, `Terminal` и `FitAddon`. Отдельный xterm.js/fallback renderer отсутствует. Один общий React component lazy-load-ит Ghostty-Web, применяет platform theme, включает bounded scrollback, copy/paste и refit через `ResizeObserver`; hidden → visible transition дополнительно выполняет один frame-delayed `fit()`.

Transport для host и container PTY одинаков: Access-authenticated WebSocket на admin hostname, initial bounded `cols/rows`, binary server→client output, binary client→server input и text JSON control frame только для `{type:"resize",cols,rows}`. Размер ограничен `1..1000` columns и `1..500` rows. Exact Origin, Access JWT и permission проверяются до upgrade; connection не переносится между daemon processes и не имеет replay/resume. При WebSocket close backend закрывает PTY и всю process group/session. Различается только spawn adapter: host shell §16.2 либо attached libpod exec §16.1.

### 16.1. Container terminal

Admin может открыть PTY через libpod exec в конкретном running container. UI предлагает shell из allowlist, найденный в image (`/bin/sh`, `/bin/bash`), либо explicit command.

Сессия доступна только через admin hostname и требует действующий Access JWT. API tokens не открывают container PTY, а automation hostname не имеет attach/resize/interactive-exec endpoints. Записываются actor, project, service, container, command, start/end и exit status. Содержимое PTY не записывается.

### 16.2. Server root terminal

Interactive server console в admin UI создаёт root PTY на host. Она требует одновременно:

1. действующий Cloudflare Access JWT;
2. отдельную console passphrase.

Passphrase задаётся локально в `platformd init`. Хранится только Argon2id verifier с random salt. Plaintext существует только во время проверки и очищается из памяти best-effort.

Passphrase нельзя посмотреть, изменить или сбросить через UI/API/MCP. Reset возможен только локально командой `platformd init --reset-console-passphrase` от root.

Platformd выполняет не больше одной Argon2id verification одновременно и не отвечает на failed attempt раньше fixed 2 seconds. После пяти последовательных ошибок действует один global in-memory cooldown 60 seconds; successful verification сбрасывает counter, daemon restart также сбрасывает counter/cooldown. Per-subject/per-IP и persisted passphrase counters отсутствуют. Успешная проверка разрешает одну terminal session с idle timeout и absolute lifetime. Содержимое/keystrokes не записываются; audit содержит actor, source IP, timestamps, duration и close reason.

### 16.3. Automation root exec

REST endpoint `POST /api/v1/server/exec` и MCP tool `server_exec` выполняют одну non-interactive shell command как host root и возвращают separate bounded stdout, stderr, exit code, timeout/truncation flags и duration. Command запускается через fixed `/bin/sh -lc` в отдельном delegated cgroup; при timeout либо request cancellation platformd best-effort убивает все процессы, оставшиеся в этом cgroup subtree. API само не создаёт PTY/background mode, но arbitrary host-root command способен намеренно выйти из cgroup, создать systemd unit либо другой persistent process; отсутствие detached process не является security guarantee. Для interactive работы остаётся UI terminal §16.2.

Root exec доступен только unbound `admin` API token. Project-bound token и `read` token его не видят. Console passphrase для automation root exec не применяется: unbound `admin` token сознательно эквивалентен root credential и может прочитать master key, credentials и любые host files либо повредить platform state. UI показывает это предупреждение до создания такого token. Command и output не сохраняются; audit содержит token ID, timestamps, duration, exit/timeout/truncation без command text/output.

## 17. Public API и tokens

В admin UI можно включить отдельный automation hostname и создавать random API tokens формата `ptk_<public-id>_<256-bit-secret>`. Token показывается один раз. SQLite индексирует только random public ID и хранит HMAC-SHA-256 secret verifier под отдельным key, derived from master key; verification выполняется constant-time после одного indexed lookup. Это не password/Argon2 flow. Authentication имеет per-ID/source rate limits и revoke действует для следующего request.

Token имеет ровно одну role: `read` либо `admin`, и optional exact `projectId` restriction. `read` разрешает list/get/status, bounded logs и bounded data-browser reads без platform secrets. `admin` включает `read`, all project/resource mutations, deployments, backups, Registry management и arbitrary PostgreSQL SQL. Interactive container/server PTY остаются Access-only UI surfaces и API token не доступны. Project-bound token не видит и не изменяет resources других projects.

Unbound `admin` дополнительно получает installation-wide operations и automation root exec §16.3 и поэтому является полным root credential без console passphrase. Dedicated API endpoints для master-key/Origin-key/token-secret reveal всё равно отсутствуют, но root command может прочитать их напрямую. `read` и project-bound `admin` не получают host root exec, console-passphrase reset или platform secret reveal.

API использует versioned JSON routes `/api/v1/*`, Bearer authentication, pagination и OpenAPI document. API hostname не принимает Cloudflare Access identity как замену token.

Role и optional project boundary проверяются на каждом request до resource lookup/result serialization. SQL и `server/exec` возвращают bounded output только в исходном response/stream; server-side command/query output не сохраняется.

## 18. MCP

Platformd предоставляет stateless subset MCP Streamable HTTP на endpoint `/mcp` automation hostname. MCP использует те же API token roles/project boundary и вызывает те же application services, что REST API.

Минимальные tools:

- list/get projects and services;
- create/update/redeploy/rollback service;
- read deployment status;
- read bounded logs;
- list managed resources and backup status;
- on-demand list/search official managed database image tags and start/read `Change version` operation;
- read PostgreSQL/Redis/S3 metadata through bounded operations;
- `postgres_query` для arbitrary SQL и result output с `admin` role в пределах token project boundary;
- `server_exec` для host root command/output только с unbound `admin` token.

MCP не предоставляет interactive PTY или arbitrary container exec. `server_exec` является отдельным non-interactive root surface и может вернуть любые host secrets; этот риск равен REST root exec и принят явно.

Platformd реализует обязательный lifecycle MCP 2025-11-25 без stateful session. Initial `initialize` POST не требует `MCP-Protocol-Version` header и negotiates version из request body. Server отвечает `InitializeResult` с exact `protocolVersion`, обязательным `serverInfo {name, version}` и только capability `tools` без `listChanged`. Следующий `notifications/initialized` принимается отдельным POST и получает `202` без body, но server ничего не сохраняет. После этого client вызывает `tools/list`/`tools/call`; отсутствие session state означает, что server не пытается связать последующие calls с конкретным initialize exchange.

Каждый JSON-RPC message выполняется отдельным HTTP `POST` с UTF-8 `Content-Type: application/json`. Client обязан прислать `Accept`, содержащий одновременно `application/json` и `text/event-stream`; иначе server возвращает `406`. Request получает bounded `Content-Type: application/json` response, а accepted notification — `202` без body. Platformd не выдаёт `MCP-Session-Id`, не хранит MCP sessions/cursors/event store, не открывает SSE, не отправляет server notifications и не поддерживает stream resumption; `GET`/`DELETE /mcp` возвращают `405`. Unsupported client notifications отклоняются без создания state. Долгие действия возвращают domain/observational ID, а agent явно polling-ит status tool. Tool output, включая SQL/root exec, использует те же bounded result limits, что REST.

Каждый POST независимо валидирует Bearer token. Каждый POST после `initialize`, включая `notifications/initialized`, обязан содержать exact `MCP-Protocol-Version: 2025-11-25`; missing/invalid/unsupported header получает `400`. Native client без `Origin` принимается; если `Origin` присутствует, разрешён только exact automation origin, иначе `403`. Role/project boundary проверяется заново для каждого tool call, а deterministic `tools/list` фильтруется role. Resource-mutating tools требуют `admin` и вызывают те же application services, что REST. Stateless transport не ослабляет auth/audit и не создаёт server-side session identity.

## 19. Embedded OCI Registry

Registry реализован внутри platformd и обслуживается на configured registry hostname через listener `443`.

Поддерживаемый v1 surface:

- `GET /v2/`;
- blob `HEAD`, `GET`, Range GET;
- upload `POST`, sequential `PATCH`, status `GET`, finalize `PUT`, cancel `DELETE`;
- manifest `HEAD`, `GET`, `PUT`;
- tag listing;
- OCI image/index и Docker schema 2 manifests;
- SHA-256 digest verification;
- repository-local content-addressed blob storage; cross-repository deduplication отсутствует.

Push всегда требует HTTP Basic с generated robot token. Basic username содержит random public credential ID, password — 256-bit secret; SQLite выполняет один indexed lookup и constant-time HMAC verification с per-ID/source rate limit. Каждый RegistryCredential принадлежит ровно одному repository и имеет fixed `pull` либо `pull+push` permission; multi-repository scopes отсутствуют. Каждый upload request заново авторизуется и сверяет upload session repository. Private repository требует auth для pull. Для repository можно включить anonymous public pull. Secret показывается один раз.

Blob `GET/HEAD` авторизуется для exact requested repository и читает digest только из его `registry/<repository-id>/blobs/sha256/`. Private layer физически отсутствует в directory unrelated public repository, поэтому global reachability authorization и cross-repository blob links не нужны.

Upload stream-ится во temporary file данного repository с incremental digest; layer не буферизуется в RAM. Final blob публикуется atomic rename после проверки digest/size. Отдельная SQLite blob/link row не создаётся: existence проверенного immutable file внутри repository является payload state, а SQLite владеет manifests/tags/policies. Повтор того же digest внутри одного repository переиспользует тот же file. Incomplete uploads удаляются по TTL. Cross-repository blob mount отсутствует в v1: `POST .../blobs/uploads/?mount=<digest>&from=<repository>` не монтирует source blob и вместо ошибки создаёт обычную destination upload session с `202 Accepted` и `Location`, чтобы стандартный client автоматически загрузил blob полностью.

Manifest PUT имеет hard size/media-type/reference limits, разрешает только blobs, физически существующие в directory того же repository, а OCI index — только child manifests того же repository. Одной SQLite transaction он сохраняет bounded manifest bytes и tag; persistent reachability graph отсутствует. Отдельное watcher notification не отправляется: embedded registry обнаруживается обычным 10-second poll. Repository/tag lengths, concurrent uploads, uploads per token, manifest count, open files и aggregate temporary bytes имеют quotas.

Для заявленного subset platformd следует Distribution contract буквально. Поскольку v1 использует direct Basic auth без token service, `401` рекламирует `WWW-Authenticate: Basic realm="platformd registry"`; Bearer-only parameters `service`/`scope` не выдаются. Все responses под `/v2/`, включая `4xx`, возвращают `Docker-Distribution-API-Version: registry/2.0`. Successful blob/manifest `GET` и `HEAD` возвращают `Docker-Content-Digest`, одинаковые `Content-Length`/`Content-Type`, а upload responses — canonical `Location` и требуемый `Range`; tag pagination использует bounded `n`, lexical order и RFC 5988 `Link: rel="next"`. Ошибки имеют соответствующий HTTP status и Distribution JSON error code. Неподдерживаемая операция отклоняется явно, а не маскируется несовместимым успешным ответом.

Private Registry responses всегда получают `Cache-Control: private, no-store` и `Cloudflare-CDN-Cache-Control: no-store`. Для public repository digest-addressed immutable payload может cache-иться; mutable tag response требует revalidation/no-store.

Cloudflare request-size limits являются внешним ограничением. Platformd не вводит собственный 90 MiB limit и не обещает успешный push oversized single request. Если Cloudflare отклоняет request до origin, platformd не может изменить его `413` body либо добавить подсказку; limitation объясняется в UI/docs, а пользователь отвечает за размер layers/client chunking.

Online GC отсутствует. UI предоставляет explicit `Registry cleanup`, которая для каждого repository вычисляет references из его current manifests и удаляет только unreferenced files в том же repository при остановленных registry mutations и после dry-run preview. Межрепозиторного graph/lock нет.

### 19.1. Admin browser и deletion

Admin UI показывает repositories с name, public/private pull policy, tag/manifest count, referenced и total local blob bytes, last push и backup status. Repository view показывает images как OCI/Docker manifests: digest, tags, media type, platforms для index, pushed timestamp, manifest size и суммарный referenced blob size. Manifest JSON и blob digests доступны как bounded read-only details; layer contents не preview-ятся.

Destructive actions используют exact repository mutex:

- `Delete tag` одной SQLite transaction удаляет только tag binding; manifest и blobs остаются.
- `Delete image` означает удаление exact manifest digest: transaction удаляет manifest metadata и все tags этого repository, которые указывают на digest. Если manifest является child действующего OCI index, deletion отклоняется с перечнем parent index digests; operator сначала удаляет parent image. Blob files не удаляются автоматически и становятся candidates для explicit `Registry cleanup`.
- `Delete repository` требует ввода exact repository name, сначала запрещает новые pull/push/tag/upload requests этого repository и ждёт bounded drain уже открытых requests/uploads. Timeout снимает запрет и возвращает `409 repository_busy` до metadata commit. После drain одна SQLite transaction удаляет repository, manifests, tags, policies, credentials и upload sessions. После commit repository directory больше не reachable через Registry и удаляется целиком; crash оставляет только orphan directory, который startup cleanup удаляет, потому что соответствующей SQLite repository row уже нет.

Прямого `Delete blob` нет: blob lifecycle определяется repository deletion либо explicit cleanup по manifest reachability. Все actions доступны через protected admin UI и тот же authorized REST application service; audit сохраняет actor, repository, affected digest/tags и result, но не manifest body.

## 20. Registry backup

Registry имеет собственную BackupPolicy: scheduled backup default disabled, individual schedule и retention count. `Backup now` запускает только Registry и разрешён независимо от scheduled toggle, если remote target существует. Backup на всё время держит existing cleanup exclusion, а на время enumeration закрывает admission manifest/tag/repository publication/deletion. Пока admission закрыт, metadata одного Registry логически неизменна, поэтому platformd читает её stable-primary-key pages через последовательность коротких SQLite read transactions и материализует exact repositories/manifests/tags/policies и referenced repository-local blob paths в transient encrypted manifest под `backups/work/`. Долгой SQLite snapshot transaction нет, WAL не pin-ится. После завершения enumeration metadata admission снова открывается; upload chunks и pulls, не меняющие published metadata, блокировать не требуется.

Дальнейший backup копирует blobs только из материализованного списка в новый independent Registry generation prefix. Cleanup exclusion остаётся до окончания копирования, поэтому перечисленные immutable files не исчезнут, а push/tag mutations уже могут продолжаться. Payloads из старых generations не переиспользуются; durable pins и abandoned-pin cleanup не нужны. Completion marker публикуется последним, transient local manifest удаляется, incomplete remote prefix очищается после TTL.

Registry retention удаляет целиком complete generation prefixes сверх собственной policy и никогда не затрагивает incomplete/running generation. Shared remote payloads, reachability graph и remote GC отсутствуют. Individual restore выбирает любую complete Registry generation в UI/API, включает maintenance, устанавливает/проверяет blobs в соответствующие repository directories и одной SQLite transaction публикует tags/manifests/policies. Ошибка до transaction оставляет только безопасные unreferenced repository-local files для последующего local cleanup; старый catalog остаётся активным. Remote manifest является transport/recovery format, но не вторым authoritative catalog.

## 21. Embedded private S3

Object store реализован внутри platformd. Каждый ObjectStore принадлежит project, содержит ровно один immutable `bucketName` и всегда доступен по internal DNS, например:

```text
http://assets.shop.internal:9000
```

Каждый ObjectStore физически владеет отдельным `objects/<store-id>/` с `payloads/` и `multipart/`. Payload/chunk другого store никогда не referenced и не переиспользуется: store-specific encryption key уже исключает полезный cross-store dedup. Delete, temporary cleanup, restore и disk accounting всегда ограничены одним store directory; глобального object payload graph нет.

Опционально в UI store получает отдельный public hostname, например `objects.example.com`. Он обслуживается тем же listener `443` через Cloudflare. Cloudflare Access на S3 hostname не используется, потому что он несовместим с обычными S3 clients и presigned URLs.

Platformd привязывает один S3 listener к actual gateway IP каждой project bridge, например `10.90.1.1:9000`; несколько stores этого project маршрутизируются по exact internal hostname. Listener не bind-ится к `0.0.0.0`. Netavark/nftables policy разрешает его только с соответствующего project bridge. Storage metadata и payload остаются single-writer state platformd; sidecar/gateway container отсутствует.

Минимальный S3-compatible surface:

- path-style requests только к fixed bucket этого ObjectStore;
- `HEAD Bucket` и `ListObjectsV2`;
- PUT, GET, HEAD и DELETE object;
- Range GET;
- multipart `CreateMultipartUpload`, sequential/parallel `UploadPart`, paginated `ListParts`, `CompleteMultipartUpload` и `AbortMultipartUpload`; bucket-wide `ListMultipartUploads` отсутствует;
- SigV4 header authentication;
- bounded presigned GET/HEAD/PUT URLs для internal либо configured public hostname.

Поддерживаемый SigV4 profile фиксирует AWS4-HMAC-SHA256 canonical URI/query/header rules, duplicate query handling, service `s3`, configured region, clock-skew limit и `UNSIGNED-PAYLOAD` для presigned requests. Header-auth PUT поддерживает signed SHA-256 payload; AWS streaming-chunked SigV4 не поддерживается в v1 и отклоняется явно. Multipart/checksum modes публикуются в compatibility docs и проверяются AWS SDK contract tests.

Fixed v1 limits/semantics:

- maximum plaintext object size — 100 GiB; Cloudflare может установить меньший effective public-request limit;
- object payload шифруется fixed 4 MiB chunks;
- поддерживается только один byte range; multi-range отклоняется, а decrypt amplification составляет не более двух boundary chunks сверх requested bytes;
- `ListObjectsV2` возвращает не больше 1000 entries и opaque continuation token;
- multipart содержит не больше 10 000 parts, каждая part кроме последней минимум 5 MiB, maximum part size 512 MiB;
- `UploadPart` возвращает quoted lowercase SHA-256 plaintext part; `ListParts` возвращает те же ETags, а `CompleteMultipartUpload` требует ordered unique part numbers и exact ETag каждой выбранной part;
- final object ETag является quoted lowercase SHA-256 полного plaintext object и не обещает AWS MD5/multipart-MD5 semantics;
- количество objects в одном ObjectStore platformd специально не ограничивает; пределом являются SQLite/filesystem/disk pressure, поэтому list/backup/restore очень большого store может занимать долгое время.

S3 `CreateBucket`, `DeleteBucket` и `ListBuckets` отсутствуют: lifecycle выполняется через ObjectStore UI/API, а для второго bucket создаётся второй ObjectStore. Request с bucket name, отличным от configured exact `bucketName`, получает `NoSuchBucket`. Также не поддерживаются ACL, IAM policies, bucket policy, public-read, website hosting, tagging, Object Lock, replication, events, Select и virtual-hosted buckets.

Access key/secret создаются UI, secret показывается один раз. Stored secret шифруется master key, потому что SigV4 требует исходный secret.

Каждый access key принадлежит ровно одному ObjectStore и его единственному bucket. Indexed lookup возвращает `storeId`; routed store из exact Host/internal endpoint и bucket path обязаны совпасть. Credential имеет fixed read-only либо read-write mode только внутри этого store; cross-store/project access запрещён, revoke действует со следующего request.

Committed object metadata является current state и ссылается на immutable encrypted payload/chunks. Overwrite либо DELETE одной SQLite transaction заменяет/удаляет только metadata; прежний payload становится orphan и физически не удаляется inline. Multipart completion следует тому же правилу. `ObjectStore cleanup` вычисляет current references только внутри выбранного store и удаляет unreferenced payloads; explicit UI action сначала показывает preview, а тот же algorithm может автоматически запускаться disk-pressure policy. Cleanup сериализуется с backup/restore, но ordinary PUT/DELETE между собой используют короткие metadata transactions. Cross-store graph и local generation model отсутствуют.

`Private-only` означает authorization-private, а не обязательно network-private: anonymous request всегда получает отказ, object нельзя сделать public-read, а внешний доступ возможен только с valid SigV4 либо неистёкшей presigned URL. Signature привязана к exact public hostname, method, path, expiry и подписанным headers. Default maximum presign lifetime — 1 hour, hard maximum — 7 days. Public S3 responses не кэшируются Cloudflare по умолчанию. Presigned PUT подчиняется Cloudflare request-size limits.

Public authenticated/presigned responses принудительно содержат `Cache-Control: private, no-store` и `Cloudflare-CDN-Cache-Control: no-store`. Optional per-store CORS allowlist поддерживает только configured origins, `GET/HEAD/PUT` и необходимые signed headers; preflight `OPTIONS` не требует SigV4, но проверяет allowlist. Без CORS configuration browser cross-origin fetch/PUT запрещён.

### 21.1. Local encryption

Object payload и incomplete multipart parts всегда encrypted at rest. Для каждого ObjectStore единственный 32-byte key детерминированно выводится как `HKDF-SHA-256(masterKey, salt=storeId, info="platformd/s3/store/v1")` и отдельно не сохраняется. Final object делится на fixed-size chunks; multipart temporary chunks используют тот же size. Каждый final chunk шифруется `XChaCha20-Poly1305` тем же store key с новым CSPRNG-generated random 24-byte nonce и associated data `(formatVersion, storeId, immutablePayloadId, chunkIndex, plaintextSize)`. Multipart chunk использует отдельный associated-data domain `(formatVersion, storeId, uploadId, partNumber, chunkIndex, plaintextSize)`. На диске сохраняются только nonce, ciphertext и authentication tag; plaintext temporary parts запрещены, corruption обнаруживается до выдачи plaintext. Вероятностная уникальность 192-bit nonce принимается, отдельный nonce registry не вводится; deterministic nonce запрещён.

При multipart completion platformd последовательно decrypt-ит ordered parts, проверяет их declared sizes/checksums, вычисляет final plaintext SHA-256 ETag и stream-ит данные в новые final encrypted chunks с новыми nonces; metadata становится visible только после полного fsync/verification. Temporary parts удаляются после commit либо становятся cleanup candidates после crash.

Offsite S3 backup копирует local ciphertext chunks byte-for-byte; повторное backup encryption к ним не применяется. На новой VPS тот же store key выводится из сохранённого master key и `storeId`, а S3 metadata manifest шифруется common backup resource key §25.

Master-key rotation и отдельная ObjectStore-key rotation в v1 отсутствуют. Изменение master key потребовало бы maintenance и полного перешифрования всех S3 payloads, поэтому существующий key автоматически не меняется и UI такой операции не предоставляет.

Atomic metadata commit делает видимым только полностью записанный object. Temporary multipart data имеет TTL. Encryption не защищает от root attacker на работающей VPS; её цель — отсутствие plaintext payload на диске и безопасное offsite копирование.

### 21.2. Data browser

Admin UI показывает configured bucket, prefixes, objects, size, content type, ETag и timestamps. Разрешены bounded text/image preview и explicit download. Secret-like/binary content автоматически не открывается.

## 22. Object-store backup

Каждый ObjectStore имеет собственную BackupPolicy: scheduled backup default disabled, individual schedule и retention count. Backup на всё время держит per-store cleanup exclusion, а на время metadata enumeration закрывает admission PUT/DELETE/multipart-complete для exact store. Уже идущие upload body могут продолжать писать encrypted temporary chunks, но их final metadata commit ждёт открытия admission.

Пока store metadata логически неизменна, platformd читает object rows и referenced immutable payload/chunk IDs stable-primary-key pages через последовательность коротких SQLite read transactions и записывает transient encrypted manifest под `backups/work/`. Долгой SQLite snapshot transaction нет, поэтому другие product writers не раздувают удерживаемый WAL. После окончания enumeration metadata admission открывается и ordinary PUT/DELETE продолжаются. Backup копирует перечисленные payloads в новый independent store generation prefix; cleanup exclusion остаётся до completion marker, поэтому snapshot payload физически не исчезнет. Pins, local generations и abandoned-pin cleanup отсутствуют. Store retention удаляет целиком complete remote prefixes сверх собственной policy; chunks между backup generations не переиспользуются, shared remote chunks и reachability GC отсутствуют.

Individual restore выбранной complete remote generation берёт in-memory per-store maintenance gate, блокирует PUT/DELETE/multipart completion/cleanup и ждёт bounded drain. Затем устанавливает chunks как невидимые/unreferenced payloads, проверяет manifest и authentication tags и одной SQLite transaction заменяет current object metadata выбранного bucket. Product object-count cap и fixed transaction deadline отсутствуют: для очень большого store эта final transaction может долго удерживать SQLite writer, а admin UI показывает restore progress. Ошибка/interrupt до commit полностью rollback-ит metadata transaction, оставляет прежние rows активными и только unreferenced payloads для cleanup; crash после commit использует новые rows. Generation pointer, staging metadata tables и in-place partial merge отсутствуют; remote manifest не становится runtime authority.

## 23. Managed databases и image selection

### 23.1. Общая модель версий

Встроенного либо release-managed каталога версий и default version нет. Managed profiles принимают только готовые official images из compile-time allowlist:

```text
docker.io/library/postgres
docker.io/library/redis
```

При create либо `Change version` UI on demand запрашивает paginated tag list соответствующего repository через OCI Distribution/Docker Hub, предоставляет search и explicit manual tag input. Это stateless remote lookup: tag list не сохраняется в SQLite, release files или отдельном cache catalog. Ошибка tag listing не запрещает manual input.

Перед Apply platformd resolve-ит selected tag в OCI index, выбирает exact manifest digest текущей host architecture, проверяет Linux/architecture манифеста и сохраняет `(engine, imageTag, imageDigest)`. Runtime и backup/restore jobs всегда используют сохранённый digest; tag остаётся только display/provenance value. Floating execution и automatic managed-database image update отсутствуют. Если tag позже переместился, UI может показать новый digest, но resource не меняется, пока пользователь явно не запустит `Change version`/`Update image`.

Platformd не пытается извлечь или сравнить semantic database version из произвольного tag. Пользователь может выбрать любой tag allowlisted official repository, включая повтор того же tag, если его current digest изменился. Apply разрешён только когда target digest отличается от active digest. Отдельных defaults, newer/downgrade rules, compatibility matrix и release-tested version list нет.

Тот же engine-level adapter фактически проверяет выбранный image через initialization, health и direct data-transfer workflow. Несовместимый image/format завершается failure до publication и возвращает old resource. Если сохранённый digest отсутствует одновременно в local image cache и upstream registry, platformd не подставляет новый digest того же mutable tag: resource/restore остаётся stopped с explicit `image_digest_unavailable`, пока operator не вернёт image в registry/cache либо сознательно выберет другой tag через migration workflow.

Engine-level minimum memory и persisted database disk quota отсутствуют; optional user limits применяются как есть, а disk-growing operations проверяют actual host free space. Custom repository/image для managed profile запрещён; для него пользователь создаёт ordinary Service без managed guarantees.

### 23.2. Change version

UI/API предоставляет explicit `Change version`/`Update image` operation с downtime. Любое explicit изменение managed image digest всегда использует новый volume + direct data transfer. Пользователь не определяет, изменился ли on-disk format; безопасный migration path применяется и для major, и для patch update, и для переместившегося того же tag. In-place reuse data directory и `pg_upgrade` отсутствуют. Remote backup target для операции не требуется.

До запуска UI показывает source/target tags и exact digests, current database/RDB size, необходимое free space для одновременного old + new volume и предупреждение о недоступности database. Downtime не оценивается: throughput dump/restore заранее не моделируется. Разрешён любой allowlisted official target того же engine с digest, отличным от active; semantic upgrade/downgrade platformd не классифицирует. Совместимость определяется фактическим transfer/load.

Operation:

1. Resolve-ит target tag для host architecture, проверяет allowlisted repository, Linux/architecture manifest, image availability, health source и actual host free space для второго volume; optional CPU/memory limits target наследует от resource.
2. Берёт in-memory per-database maintenance gate, убирает её DNS record и nftables блокирует новые project connections к database endpoint, оставляя доступ только exact internal update job.
3. Ждёт bounded drain, затем завершает оставшиеся client sessions.
4. Создаёт new volume и isolated target без stable DNS record.
5. PostgreSQL stream-ит `pg_dump` source напрямую в `pg_restore` target; Redis выполняет final `SAVE`, останавливает source, копирует RDB напрямую из old volume в new volume и запускает target. Backup artifact локально или в remote S3 не создаётся.
6. Выполняет engine-specific health/data checks и останавливает PostgreSQL source, если он ещё работает.
7. Одной SQLite transaction переключает единственный active `(imageTag, imageDigest, volumeId)` pointer на target; in-memory DNS публикует прежнее stable hostname с new IP, после чего maintenance gate снимается.
8. Old volume сразу становится unreferenced и удаляется; crash между pointer switch и удалением завершает тот же orphan cleanup при следующем startup.

Failure до шага 7 автоматически удаляет/останавливает target и оставляет либо перезапускает old container/DNS. После публикации target может принимать writes, а old volume удаляется, поэтому rollback возможен только через complete remote backup, если он существует; при disabled/missing backup возврата данных нет. UI явно предупреждает об этом до version change. Platformd не пытается обнаруживать или останавливать «зависимые сервисы» через сложный dependency graph: network gate database endpoint является единственной maintenance boundary.

В database row сохраняется только active `(imageTag, imageDigest, volumeId)` pointer; maintenance, source/target IDs и publication status в SQLite отсутствуют. Candidate DB volume не является SQLite resource и защищён от live cleanup per-resource mutex только на время текущей operation. После daemon crash in-memory gate исчезает, startup строит referenced set из ordinary Volume rows и active managed-DB volume pointers, удаляет все unreferenced candidate/old DB directories и runtime objects и запускает active pointer. Поэтому crash до pointer switch возвращает old resource, а crash после switch использует target. Concurrent backup, delete, second version change и self-update для этой database блокируются только in-memory per-resource gate текущего daemon process.

Для PostgreSQL short-lived job из target image выполняет direct `pg_dump(source) → pg_restore(target)` в новый initialized cluster. Managed profile допускает одну generated owner role/database; application role не получает `CREATEROLE`, `CREATEDB`, superuser или tablespaces, поэтому произвольные cluster globals не являются частью managed contract. Target сначала пересоздаёт generated role/database с теми же platform credentials, затем `pg_restore --no-owner` восстанавливает objects под managed owner. Generated database settings применяются заново из SQLite config.

Отдельного preflight для installed extensions/collations и representative row-count validation нет. `pg_dump`/`pg_restore` exit status, target health и basic connection/schema checks определяют success; несовместимый переход, unsupported source/target pair либо user-created extension/collation обнаруживается как migration failure, после чего old resource возвращается. Pairwise release tests и catalog validation отсутствуют.

Для Redis после блокировки clients создаётся согласованный RDB и напрямую копируется в new volume выбранного target image. Несовместимость определяется load/health failure с возвратом old resource.

### 23.3. Managed PostgreSQL

Managed PostgreSQL является opinionated container profile, а не встроенной database engine.

Создание включает:

- name/project, selected official image tag и exact host image digest;
- generated database/user/password;
- optional CPU/memory hard limits;
- persistent volume;
- internal hostname `<name>.<project>.internal:5432`;
- individual BackupPolicy;
- health check.

Credentials показываются один раз и доступны service binding через secret references. Database port не публикуется на host/public route.

Runtime profile всегда mount-ит managed volume в `/var/lib/postgresql/data`, явно задаёт `PGDATA=/var/lib/postgresql/data/pgdata` и сохраняет default `docker-entrypoint.sh`/`postgres` official image. Так один product-owned data path работает и с images до PostgreSQL 18, и с новым official layout PostgreSQL 18+. Platformd задаёт `POSTGRES_USER=postgres`, `POSTGRES_DB=postgres` и `POSTGRES_PASSWORD=<random bootstrap password>`. Этот internal superuser credential зашифрован в SQLite, никогда не выдаётся application/UI/API и используется только lifecycle adapter-ом.

После official entrypoint initialization platformd native PostgreSQL client-ом создаёт отдельную generated `LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION` owner role и единственную application database с этой role как owner. Наружу выдаются только owner credentials. Readiness означает successful password-authenticated connection и `SELECT 1` именно от owner role к application database; один TCP-open или `pg_isready` недостаточен. Create и target version-change публикуют resource только после этого end-to-end probe; так произвольный allowlisted tag проверяется без release catalog и хрупкой static inspection его entrypoint internals.

#### 23.3.1. Backup

При manual либо scheduled backup internal worker запускает short-lived job из exact PostgreSQL runtime image и от owner role выполняет `pg_dump --format=custom --no-owner --no-acl` для application database. Stdout сразу stream-ится через encryption в собственный remote generation prefix; platformd требует zero exit status и записывает checksum/metadata. Restore создаёт пустую owner/database тем же profile и запускает `pg_restore --exit-on-error --no-owner --no-acl` от owner role; nonzero exit никогда не публикует candidate.

BackupPolicy каждой database по умолчанию disabled и независимо задаёт schedule/retention §25. `Backup now` доступен при configured remote target независимо от scheduled toggle. PITR, WAL archiving, physical backup и replicas отсутствуют. Version change выполняется только workflow §23.2 и backup generation не создаёт.

Individual restore выбирает complete generation этой database и создаёт новую instance/volume либо требует explicit destructive replacement после confirmation. Backup считается successful только после remote checksum verification.

#### 23.3.2. Data browser

UI предоставляет:

- schemas/tables/views list;
- columns, indexes и approximate row counts;
- paginated rows с server-side limit;
- полноценный SQL editor/console в стиле PlanetScale.

SQL console доступна interactive admin через Cloudflare Access, REST `admin` token и MCP `postgres_query`. Project-bound token может обращаться только к PostgreSQL своего project; unbound `admin` — к любой managed PostgreSQL. Console выполняет arbitrary SQL, включая multi-statement, transactions, DDL и DML, от generated managed owner role внутри выбранной database; PostgreSQL superuser/cluster-level privileges не выдаются. Platformd не парсит и не классифицирует SQL. Server одинаково для UI/API/MCP ограничивает concurrent queries, result rows/bytes, idle session и maximum execution time, поддерживает streaming/bounded results и cancellation. API/MCP возвращают columns, rows либо command tag/affected rows и structured error. Query text/history server-side не сохраняется и в audit не попадает; audit содержит actor/token, database, timestamps, duration, result/row count и error class без row values.

## 24. Managed Redis

Managed Redis выбирает official image tag по §23.1 и сохраняет tag вместе с pinned host image digest. Он использует persistent volume, generated password, optional CPU/memory limits и internal hostname `<name>.<project>.internal:6379`.

В v1 поддерживается одна Redis instance без Sentinel, Cluster, replica или automatic failover.

Version change выполняется только workflow §23.2; floating tag и automatic update отсутствуют.

Runtime profile mount-ит managed volume в `/data` и generated file из `/run/platformd/generated/<resource-id>/redis.conf` как read-only `/run/platformd/redis.conf`. Host directory имеет mode `0700`, а file — `0444`: official entrypoint сначала drop-ит root до user `redis`, поэтому config должен быть readable внутри этого container; host directory не traverse-ится не-root process и file не монтируется в другие workloads. Generated config явно задаёт `daemonize no`, `bind 0.0.0.0`, `protected-mode yes`, `port 6379`, `dir /data`, `dbfilename dump.rdb`, `appendonly no`, `save 300 1` и `requirepass <generated password>`. Platformd сохраняет default official entrypoint, но запускает exact command `redis-server /run/platformd/redis.conf`. Generated file не попадает в logs/audit и удаляется при restart вместе с `/run/platformd/generated/`.

Readiness означает successful `AUTH` generated password и `PING` через native RESP client platformd; одно TCP-open недостаточно. Create и target version-change публикуют resource только после загрузки exact config и этого end-to-end probe. Official image, который не принимает этот baseline config/RDB, завершает create/change с explicit compatibility error до publication.

Local persistence — RDB-only: `appendonly no`, fixed `save 300 1` policy пытается создать snapshot каждые 5 minutes при наличии writes, а graceful stop требует final `SAVE` с bounded timeout. Target RPO в healthy steady state — 5 minutes, но hard upper bound отсутствует: BGSAVE может идти долго или fail. Actual RPO равен age последнего successful RDB; UI/alert показывает его. AOF/PITR guarantee отсутствует.

### 24.1. Backup

Platformd не использует изменение second-resolution `LASTSAVE` как единственный completion signal. Перед новым snapshot он ждёт окончания уже выполняющегося BGSAVE, открывает existing `dump.rdb`, если он есть, и сохраняет его `(device,inode)` через `fstat`, удерживая old FD открытым. Затем отправляет `BGSAVE` и требует accepted response. Worker polling-ит `INFO persistence`, пока `rdb_bgsave_in_progress=0` и `rdb_last_bgsave_status=ok`; timeout/error означает failed backup.

После success platformd один раз открывает current `dump.rdb`, проверяет regular-file/size bounds и требует, чтобы `(device,inode)` отличался от удерживаемого old file либо old file отсутствовал. Redis создаёт snapshot во temporary file и atomically заменяет pathname, поэтому новый inode вместе с successful BGSAVE status однозначно относится к завершённому snapshot даже в ту же секунду. Old FD закрывается, а new FD до EOF stream-ится через encryption в remote S3 и не переоткрывается по path. Затем platformd проверяет remote checksum/read-back. AOF не экспортируется и PITR не обещается.

BackupPolicy каждой Redis instance по умолчанию disabled и независимо задаёт schedule/retention §25. `Backup now` доступен при configured remote target независимо от scheduled toggle. Backup successful только после remote read-back/checksum verification.

Individual restore выбирает complete generation этой Redis instance и создаёт новую instance/volume либо выполняет explicit destructive replacement в maintenance mode.

### 24.2. Data browser

UI использует incremental `SCAN`, а не blocking `KEYS`. Он показывает key, type, TTL и estimated size. Value preview имеет type-aware commands и строгий element/byte limit. Pub/Sub monitor и unrestricted command console отсутствуют в read-only browser.

## 25. Remote S3 backups

Platformd v1 использует максимум один installation-wide S3-compatible backup target с endpoint, region, bucket, prefix и credentials. Все resource/control backups используют его; per-resource targets отсутствуют. Target должен находиться вне этой VPS; embedded S3 этой же installation отклоняется.

Create/replace target сначала выполняет stateless probe вне SQLite transaction: пишет random test object под configured prefix, проверяет `HEAD`, скачивает и сравнивает bytes через `GET`, видит object через `LIST`, затем удаляет его. TLS verification обязательна. Только если все операции успешны, одна SQLite transaction сохраняет target и encrypted credentials; при failure новый target не существует, а при replace прежний остаётся неизменным. Create/replace/delete target получает `409 backup_target_busy`, пока выполняется любой resource backup/restore, control snapshot/import или retention; running action никогда не продолжает с удалённым либо частично заменённым target. Persistent capability/readiness state и periodic re-probe отсутствуют. Последующие remote ошибки принадлежат конкретному backup job.

PostgreSQL, Redis, Registry и каждый ObjectStore имеют независимую BackupPolicy с тремя user-facing fields: `enabled` default `false`, `cron` и `retentionCount`. `retentionCount` является integer `1..100`; zero/negative/out-of-range отклоняются, поэтому successful newly published generation никогда не удаляет сама себя. `cron` использует только пятичастный Unix format `minute hour day-of-month month day-of-week` и всегда вычисляется в UTC. Разрешены numeric values, `*`, lists, ranges и positive steps в пределах `minute=0..59`, `hour=0..23`, `day-of-month=1..31`, `month=1..12`, `day-of-week=0..6`; weekday `0` означает Sunday, а `7` отклоняется. Если day-of-month и day-of-week оба ограничены, occurrence наступает при совпадении любого из них по Vixie cron semantics. Seconds, names, `@daily`-подобные macros, `TZ=` и per-policy/installation timezone отсутствуют. `Backup now`, generation list, retention и restore относятся к одному exact resource.

Один internal worker выполняет backups/control snapshots последовательно, без FIFO, pending set и queued Operations. Когда worker свободен, он строит due candidates: dirty control snapshot с его in-memory `dirtySince` и последние UTC cron occurrences resources из BackupPolicy/Backup history, затем deterministic выбирает globally oldest due timestamp. Поэтому частые configuration mutations не дают control snapshots бесконечно вытеснять resource backups. Для каждого resource рассматривается только последний scheduled occurrence после старта текущего daemon; occurrence, уже имеющий started Backup record, повторно не запускается. Если несколько occurrences наступили, пока worker был занят, для resource выполняется только последний; occurrences до daemon startup не replay-ятся. После завершения worker заново вычисляет due set из authoritative policies/history.

`Backup now` принимается только свободным worker и сразу создаёт running Backup record; занятый worker возвращает `409 backup_worker_busy`, поэтому manual request никогда не существует в ожидающем эфемерном состоянии. Без configured target scheduled jobs не запускаются, а `Backup now` возвращает validation error. `Change version` от backup target не зависит.

Каждая resource generation имеет собственный independent prefix, self-describing encrypted manifest/data и completion marker, записанный только после immediate remote read-back/decrypt/checksum verification. Retention удаляет целиком complete prefixes сверх policy; incomplete prefix удаляется после TTL. Payloads между generations/resources не переиспользуются, поэтому shared reachability graph и remote GC отсутствуют.

Единственный master key является installation root key и единственным ключом, копию которого operator должен хранить вне VPS. Для каждого resource рабочий backup key детерминированно выводится как `HKDF-SHA-256(masterKey, salt=resourceId, info="platformd/backup/v1")` и отдельно не сохраняется; online master-key rotation отсутствует. Каждый backup chunk шифруется `XChaCha20-Poly1305` с новым CSPRNG-generated random 24-byte nonce и associated data `(formatVersion, resourceId, generationId, chunkIndex, plaintextSize)`. PostgreSQL `pg_dump` stream и копия Redis RDB шифруются до записи в `backups/work`, поэтому plaintext backup file там не сохраняется. S3 chunks уже encrypted at rest по §21.1 и копируются byte-for-byte; S3 metadata шифруется derived backup key.

Master key никогда не отправляется в backup target. Сохранённая вне VPS копия нужна для восстановления после полной потери VPS; потеря одновременно VPS и этой копии делает backups невосстановимыми. Master-key rotation в v1 отсутствует.

### 25.1. Control-plane recovery snapshot

Если backup target существует, successful product configuration mutation после SQLite commit устанавливает in-memory `controlSnapshotDirty`; при переходе `false → true` запоминается `dirtySince`, а последующие mutations автоматически объединяются и не сдвигают timestamp. Выбрав control candidate, worker атомарно очищает dirty flag **до** чтения SQLite. Mutation во время snapshot снова установит flag и не будет потеряна; failed upload/read-back снова устанавливает flag, если более новая mutation ещё не сделала этого. Backup progress, audit-only и runtime-status changes dirty flag не устанавливают. Создание target и каждый daemon startup с configured target устанавливают flag/time, поэтому потеря памяти при restart не оставляет control state без следующего snapshot. Scheduled toggle и durable control queue отсутствуют.

Remote target хранит ровно одну complete control generation. Пока новая generation загружается/проверяется, предыдущая complete остаётся доступной. После публикации нового completion marker предыдущая complete generation удаляется целиком; incomplete prefixes очищаются по TTL. Full restore всегда выбирает эту единственную latest complete generation, а восстановление более старой platform configuration не поддерживается.

Control payload целиком шифруется domain-separated key `HKDF-SHA-256(masterKey, salt=installationId, info="platformd/backup/control/v1")`. Незашифрованный envelope содержит только format version, public `installationId`, generation ID и необходимые non-secret nonce/chunk descriptors; эти fields входят в AEAD associated data. Consistent SQLite online-backup image, control manifest с exact platformd version/schema/architecture/resource IDs/checksums, exact signed release manifest и self-contained `platformd` binary записываются только как XChaCha20-Poly1305 encrypted chunks. Поэтому не только manifest, но и весь SQLite control image защищён до отправки в remote S3. Runtime helpers отдельно не сохраняются, потому что находятся в self-extracting bundle того же binary; database catalog отсутствует. Resource payloads, pulled images, logs и application volumes в control generation не входят. Control completion marker независим от resource backups.

Full restore разрешён только на той же CPU architecture, что записана в latest complete control generation. Source и destination могут быть разными supported hosts (Ubuntu 24.04 ↔ Debian 13), потому что snapshot содержит общий Linux/amd64 artifact; новый host всё равно обязан пройти §3.2 probes. Cross-architecture restore отклоняется до изменения local state. Fresh restore проверяет сохранённый release artifact pinned Ed25519 key, SHA-256, `os=linux` и architecture, до импорта SQLite atomically устанавливает его в release slot и перезапускается в recorded release. Другой binary допускается только при explicit tested `import-from` compatibility; latest binary не импортирует arbitrary old SQLite snapshot напрямую.

После полной потери VPS operator запускает `platformd init --restore` и через interactive root-only input предоставляет сохранённый master key и remote S3 endpoint/region/bucket/prefix/credentials. Bootstrap stateless читает remote manifests, выбирает latest complete control generation, проверяет architecture/signature/checksums, расшифровывает snapshot и импортирует его до запуска daemon. В той же import transaction supplied target configuration заменяет восстановленную BackupTarget row, а supplied credentials шифруются восстановленным master key. Поэтому resource restore продолжает использовать только что проверенный target, а не потенциально revoked historical credentials. Crash до import просто требует повторить `init --restore`; отдельного durable credential/resume marker нет. Master key и credentials не передаются browser и не хранятся в browser storage.

Control import восстанавливает admin hostname, Origin certificates, Access team domain/AUD, console-passphrase verifier и остальные product settings, применяет explicit bootstrap overrides из §5 и затем одной transaction устанавливает `recovery_mode=true`. Issuer/JWKS URL всегда заново выводятся из effective team domain. Operator заранее направляет прежние Cloudflare DNS records на новую VPS и настраивает provider firewall; повторная bootstrap identity и её merge отсутствуют. После запуска сохранённого exact binary admin сразу получает обычную рабочую панель через восстановленный hostname/Access. Панель показывает automatic restore progress и позволяет retry либо выбрать более старую individual generation, но initial control restore и ввод S3 credentials через browser отсутствуют.

Импортированный control state запускается с durable `recovery_mode=true`; public application routes и ordinary service reconciliation выключены. Для каждого PostgreSQL, Redis, Registry и ObjectStore из control config platformd независимо выбирает latest complete generation этого resource ID. Если generation существует, она восстанавливается и проверяется; если backups были disabled либо complete generation отсутствует, resource автоматически создаётся пустым с восстановленной configuration/credentials. PostgreSQL/Redis restore jobs остаются isolated до проверки. Общего backup timestamp и cross-resource atomicity нет; UI показывает source timestamp каждого resource.

Failure существующей resource generation не заменяется молча пустым resource: platform остаётся в recovery mode, а operator retry-ит generation либо выбирает более старую complete generation через обычный individual restore. После success/empty-create всех resources одна SQLite transaction автоматически устанавливает `recovery_mode=false` и запускает ordinary reconcile.

Ordinary application volumes не backup-ятся и их прежние data невосстановимы. После full restore platformd автоматически создаёт empty directory для каждого восстановленного Volume ID. После выхода из recovery mode enabled services с такими mounts запускаются обычным reconcile без дополнительного confirmation.

UI каждого resource показывает backup enabled/UTC cron/retention, last success, size, duration, next run в UTC и browser-local representation, а также last error. Отдельный scheduled restore-check отсутствует.

## 26. API/UI data safety

Table/Redis/S3 data browsers являются read-only и bounded. PostgreSQL SQL console §23.3.2 является явным исключением: она разрешает arbitrary mutations, но сохраняет server-side execution/result/concurrency limits. Data surfaces не должны:

- загружать целую большую table/keyspace/bucket в память;
- выполнять automatic full count;
- раскрывать managed credentials;
- помещать row/value/object content в audit log;
- разрешать unrestricted query language, кроме явно выделенного `postgres_query` к managed PostgreSQL от generated managed owner role по §23.3.2;
- продолжать query после browser disconnect без timeout.

Mutating PostgreSQL operations пользователь может выполнять через interactive SQL console. Для Redis/S3 и остальных resources mutations выполняются через их обычный protocol/API, container console либо собственный client внутри project network.

## 27. Resource limits и disk pressure

CPU и memory limits для Service/PostgreSQL/Redis optional и по умолчанию отсутствуют. Unset означает отсутствие platform-imposed cgroup cap (`cpu.max="max <period>"`, `memory.max="max"`); platformd не вычисляет dynamic defaults, не резервирует сумму limits и не запрещает memory/CPU overcommit. Если user задаёт limit, server валидирует только positive bounded value и применяет hard cgroup v2 limit через libpod; CPU выражается в cores/millicores, memory — в bytes с MiB/GiB UI. UI показывает configured limits рядом с host capacity/actual usage и предупреждает об overcommit, но не обещает защиту host от OOM. User-configurable PID limit и его API/UI field отсутствуют.

Per-resource disk quotas отсутствуют: application/DB volumes, Registry repositories, ObjectStores, graphroot, logs, backup work и reserve используют один общий filesystem, содержащий `/var/lib/platformd`. Separate bind mounts/filesystems внутри этого tree не поддерживаются; init/startup сравнивает `st_dev` каждого managed data root и отклоняет mismatch. Disk-pressure level вычисляется из текущего `statfs(/var/lib/platformd)`, не хранится в SQLite и определяется по худшему из byte usage и inode usage. Platformd проверяет usage fixed каждые 5 seconds и непосредственно перед каждой управляемой disk-growing operation. Entry thresholds:

- `low` при bytes либо inodes `>= 90%` used;
- `critical` при bytes либо inodes `>= 95%` used;
- `emergency` при bytes либо inodes `>= 97%` used.

Для hysteresis emergency понижается только когда оба usage `< 95%`, critical — когда оба `< 93%`, low — когда оба `< 88%`. После daemon restart initial level вычисляется напрямую по entry thresholds, без persisted previous level.

Действия:

- `low`: warning/audit transition, cleanup expired Registry/S3 uploads, temporary files, logs сверх retention/budget, unreferenced runtime images и orphan ObjectStore payloads, если соответствующий store не занят backup/restore; pulls, необходимые для desired deployment, ещё разрешены до перехода в `critical`;
- `critical`: всё из low плюс отказ новых desired deployments, pulls, Registry/S3 uploads, backups и managed DB version changes; reads, deletes, explicit cleanup и admin control plane остаются доступны. Reconcile/restart уже published `activeDeploymentId` и managed DB pointers разрешён только из cached exact image без pull и без создания нового persistent resource; отсутствие cached digest оставляет resource stopped;
- `emergency`: всё из critical, удаление preallocated reserve file и freeze всех workload cgroups; platformd/UI и операции удаления/cleanup не freeze-ятся. Уже issued dirty writes/writeback могут продолжиться, поэтому абсолютная ENOSPC guarantee отсутствует.

Init создаёт reserve file `max(1 GiB, 2% filesystem)` и не считает его usable capacity. После выхода из emergency workloads автоматически unfreeze-ятся; manual resume и durable emergency flag отсутствуют. Reserve file пересоздаётся только в normal state, если после allocation byte usage останется `< 90%`; его presence проверяется на startup. Audit записывает transitions, но не управляет level. Persistent volumes, current Registry/ObjectStore data и resource backups автоматически не удаляются. Для строгой isolation operator использует отдельный filesystem/host quotas; встроенного quota manager нет.

## 28. Security boundary

- Platformd работает root, потому что управляет rootful containers, networks и host `443`.
- Container workloads не считаются безопасной hostile multi-tenant boundary.
- Default capabilities минимизируются; `no-new-privileges` и pinned seccomp profile обязательны.
- Registry/S3/backup inputs считаются untrusted; digests, sizes и paths проверяются.
- TLS private keys, master key, backup credentials и registry tokens не монтируются в application containers.
- Runtime ownership определяется immutable labels и internal IDs, а не только human names.
- Provider firewall является обязательной внешней предпосылкой; `Full (strict)` сам по себе не доказывает Cloudflare source.
- Root terminal существенно расширяет impact украденной Access session, поэтому требует независимую local passphrase.
- Unbound `admin` API token намеренно равен host root credential: REST/MCP root exec обходят UI console passphrase. Его кража означает полную компрометацию VPS, master key и всех projects; project-bound tokens root exec не получают.
- Registry/S3/proxy и privileged runtime broker остаются в одном internet-facing root process. Это сознательно принятый v1 risk ради одного простого binary/process; strict privilege separation отсутствует.

## 29. Audit

SQLite audit history содержит административные mutations, token lifecycle, deployments, backup/restore actions и terminal metadata. Для события сохраняются actor, action, target IDs, timestamp, result и request correlation ID.

Audit может содержать initial container-exec command по §16.1, но не содержит PTY keystrokes/content, host root-exec command/output, SQL query text, secrets, database rows, Redis values, object content или complete request bodies. Это локальная audit history, а не tamper-proof ledger. External audit anchoring отсутствует.

AuditEvent хранится 7 дней по timestamp. Один bounded internal cleanup удаляет expired rows небольшими batches вне пользовательской mutation transaction; user-configurable retention, archive/export и legal hold в v1 отсутствуют. Cleanup audit самого себя не создаёт.

## 30. systemd

`platformd init` устанавливает единственный unit следующего назначения:

```ini
[Unit]
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=notify
ExecStart=/usr/local/bin/platformd __daemon
Restart=always
RestartSec=3
TimeoutStartSec=600
TimeoutStopSec=120
LimitNOFILE=1048576
TasksMax=infinity
Delegate=yes
DelegateSubgroup=control
KillMode=mixed
UMask=0077

[Install]
WantedBy=multi-user.target
```

Platformd отправляет `READY=1` после SQLite migration, control-plane listeners и admin UI/API readiness; broken application image не блокирует admin access. Во время bounded startup cleanup/migration он периодически отправляет `EXTEND_TIMEOUT_USEC`, не превышая общий 10-minute startup budget; после исчерпания startup завершается с ошибкой и systemd retry-ит. `KillMode=mixed` посылает SIGTERM main process: он прекращает admission и параллельно с bounded deadline graceful-останавливает application/Redis/PostgreSQL containers. После `TimeoutStopSec` systemd рекурсивно убивает remaining control group. Daemon crash/restart, normal stop, host shutdown и self-update останавливают все workloads. Container survival через control-plane restart не обещается.

## 31. Updates

Platformd поддерживает простой self-update из admin UI с полным downtime control plane и workloads. Binary содержит один compile-time fixed HTTPS release base: versioned manifest собственной version используется первым `init`, а fixed Linux/amd64 latest-manifest endpoint — UI self-update. URL/channel не настраивается. Подписывается только canonical external release manifest; binary аутентифицируется size/SHA-256 из этого signed manifest. Один offline Ed25519 release public key pinned и неизменен на всём протяжении v1; online key rotation/key IDs отсутствуют, чтобы любой сохранённый v1 control snapshot оставался проверяемым любым v1 bootstrap binary. Manifest является JSON; для проверки поле `signature` исключается, оставшийся object canonicalize-ится строго по RFC 8785/JCS и полученные UTF-8 bytes проверяются Ed25519. `version` является strict SemVer без build metadata и обязана быть строго выше installed version; повторная установка и downgrade через self-update отклоняются независимо от valid signature. Custom release channels и unattended scheduled update отсутствуют.

Минимальный external release manifest содержит только:

- `formatVersion`;
- platformd `version`;
- literal `os="linux"` и `architecture="amd64"`;
- HTTPS `binaryUrl`, positive bounded `binarySize` и lowercase-hex `binarySha256`;
- exact `supportedFrom` platformd versions;
- Ed25519 signature над всеми предыдущими canonical fields.

Все fields обязательны; unknown/duplicate keys, non-integer numbers и non-canonical field types отклоняются до signature verification. Helper checksums сюда не дублируются: они находятся в bundle manifest §3.1. Containers/storage compatibility, release-channel policy и произвольная runtime configuration отсутствуют.

Filesystem использует self-contained versioned release directories и atomic `current` symlink. `/usr/local/bin/platformd` указывает на `releases/current/platformd`; systemd запускает тот же current target. Каждый directory содержит binary, его canonical signed `release-manifest.json` и private runtime helpers/config. Manifest в slot позволяет любому previous v1 bootstrap binary проверить current version/compatibility без открытия SQLite. Второй target directory и `previous` symlink существуют только во время update до successful migration/readiness; в steady state остаётся один current release directory.

Update operation:

1. Скачивает release manifest/binary по HTTPS.
2. Проверяет Ed25519 signature, binary size/SHA-256, `os=linux`, `architecture=amd64` и membership current version в `supportedFrom`. Новый binary до switch не запускается; его appended ZIP разбирает и проверяет current binary по fixed v1 profile §3.1.
3. Self-update работает только в idle state. Одной admission boundary platformd проверяет отсутствие active deploy/backup/restore/cleanup/version-change jobs, Registry/S3 mutations, SQL/root/container exec и любых interactive container/server PTY, затем берёт global mutation gate. Если blocker уже существует, update немедленно возвращает `409 platform_busy` с bounded списком blocker kind/ID и ничего не отменяет. Read-only API, log reads/streams и ordinary application traffic update не блокируют. После взятия gate новые mutating operations/executions/sessions получают `409 platform_updating`; отдельный cancellation protocol и finish-policy state отсутствуют.
4. В staging directory записывает/fsync-ит inactive release slot целиком — binary, verified canonical `release-manifest.json` и извлечённый `runtime/` — сохраняет previous slot/version и atomically публикует target slot directory. Отдельный pre-update SQLite backup не создаётся: schema migration следующего шага transactional, до commit исходная database неизменна, а после commit rollback запрещён.
5. Old adapter только graceful-останавливает workloads с fixed deadline, не remove-ит containers/networks и не выполняет отдельный runtime/kernel cleanup. Если workloads не остановились, update abort-ится до switch и old release проходит ordinary reconcile. После successful stop platformd atomically переключает `current` и завершает daemon.
6. Новый binary до изменения SQLite выполняет exact общий startup cleanup §4: через containers/storage API unmount/delete всех container records/writable layers, verification empty container store, удаление bridges/nftables/runtime objects и recreation `/run/platformd/containers`; images/layers остаются cache. При graphroot open/validation error весь cache автоматически удаляется и создаётся empty graphroot. Libpod DB/Netavark/container actual state не мигрируются ни при каком restart. Cleanup failure происходит после switch, но до schema commit: admin UI не запускается, systemd может retry-ить тот же target, а local root может выполнить rollback.
7. Только после successful cleanup target выполняет одну bounded transactional SQLite migration. До commit database остаётся совместимой с previous release; commit является необратимой update boundary, после которой rollback previous binary запрещён. Installed version/manifest выводятся только из verified current release slot и не дублируются в SQLite. Затем target открывает control-plane listeners/admin UI, проходит local readiness и отправляет systemd `READY=1`. Только после readiness он удаляет `previous` symlink и old release directory; crash между readiness и cleanup оставляет только orphan release artifacts, которые current binary удаляет при следующем successful startup. Для каждого service/DB exact digest сначала ищется в preserved cache; только отсутствующий digest pull-ится из registry. Затем networks/containers reconciled из SQLite. Digest, отсутствующий одновременно в cache и registry, оставляет только соответствующий resource stopped с retry/error в UI и не запускает systemd restart loop.

OCI preflight, image export/import и `update-staging` отсутствуют. Preserved containers/storage обычно устраняет повторный pull, но не является availability guarantee: cache может быть очищен/несовместим, а missing digest может отсутствовать в registry. Control-plane downtime длится от остановки old daemon до admin readiness нового daemon. Downtime каждого workload длится от его остановки до cache lookup либо successful pull, recreate и health и может быть значительно дольше control-plane downtime. Zero-downtime self-update не обещается.

Atomic `current` symlink является единственной durable release-selection boundary; schema migration commit — отдельная необратимая compatibility boundary. Update marker и automatic resume отсутствуют. Crash либо graceful-stop failure до switch запускает old binary: попытка update считается прерванной, old release reconciles workloads из SQLite, а inactive target slot может быть перезаписан либо очищен следующей попыткой. Crash после switch запускает target binary, который идемпотентно выполняет общий startup cleanup, затем transactional SQLite migration и reconcile. Для прерванного до switch update operator повторно нажимает Update; cleanup/migration failure после switch требует retry либо rollback строго до migration commit.

До commit SQLite schema migration local root при остановленном unit может запустить сохранённый binary напрямую: `/var/lib/platformd/releases/previous/platformd init --rollback-update`. До переключения команда проверяет signatures/manifests обоих slots и read-only читает SQLite `PRAGMA user_version`; rollback разрешён только когда значение в точности равно schema version previous binary. После migration commit значение уже target и команда отказывает, поэтому запрет post-commit rollback обеспечивается без update marker. Затем команда atomically возвращает `current` на previous slot и запускает unit; она не зависит от того, способен ли текущий symlink target стартовать. Previous binary проходит clean-runtime startup и пытается открыть текущий graphroot. Если open fails, cache удаляется и images pull-ятся заново. Automatic rollback supervisor не вводится.

После successful SQLite commit обычный rollback запрещён. Если target не достигает readiness, сохранённый previous binary остаётся доступен **только как bootstrap verifier/installer**: local root останавливает unit и запускает `/var/lib/platformd/releases/previous/platformd init --install-signed-update <manifest> [--binary <path>]`, чтобы установить signed forward fix, version которого строго выше verified current-slot manifest и чей `supportedFrom` содержит current version, без открытия уже migrated SQLite. Previous daemon/runtime на новой schema не запускается. Если forward fix недоступен, остаётся full restore из remote control/resource backups; сложная post-commit rollback state machine отсутствует. Local previous binary автоматически удаляется только после readiness и не является долгосрочным recovery artifact.

## 32. Testing и acceptance

Обязательны:

- unit/property tests для validation, auth, digest и backup manifests;
- init interruption tests подтверждают reuse существующего master key/SQLite, отсутствие bootstrap marker/confirmation state, допустимый повтор prompt/шагов и idempotent unit/symlink enable/start/health repair обычным `init` после crash как до, так и после complete installation configuration;
- release pipeline test проверяет `build.lock.json` против exact Go/Bun/Rust/C Linux/amd64 toolchains, helper revisions/source hashes/build flags, `go.mod`/`go.sum`, absence bundled glibc/loader/private shared libraries, полный direct/transitive host SONAME allowlist, generated bundle manifest и malformed/truncated/overflow/duplicate/ZIP64/path-traversal self-extracting ZIP cases; одни release bytes проходят privileged smoke на обоих hosts;
- backup tests подтверждают independent per-resource policies/prefixes/retention `1..100`, отсутствие SQLite remote-catalog cache, bounded paginated LIST/manifests, stateless target probe, `409 backup_target_busy` при любом remote action, exact 5-field UTC cron validation/Vixie day semantics, queue-less global oldest-due selection между control/resource candidates, collapse нескольких occurrences одного resource, отсутствие replay до daemon startup, `409` для busy manual request, отсутствие lost dirty mutation во время control upload, retry после failed control upload, полное AEAD encryption SQLite/control artifacts, ровно одну complete control generation, CLI-first control import без browser bootstrap, full restore из latest generation каждого resource и automatic empty creation ordinary Volume directories;
- registry conformance tests для заявленного subset, exact Basic challenge без Bearer `service`/`scope`, API-version/digest/location/range/pagination headers, one-repository credential permission, fallback cross-repository mount в ordinary upload, repository-local blob layout, отсутствия SQLite blob links/cross-repository dedup и невозможности получить private layer через public repository; backup test подтверждает short paginated SQLite reads под closed metadata admission, transient encrypted manifest, отсутствие pinned WAL и cleanup exclusion до конца blob copy; admin tests покрывают list/details, tag deletion, protected child-manifest deletion, image deletion, bounded-drain repository cascade/timeout и orphan-directory cleanup после crash;
- S3 SDK compatibility tests для fixed pre-created bucket subset и exact object/range/list/multipart/part+object ETag contracts, включая rejection другого bucket name, encrypted multipart temporary chunks, exact Complete part-number/ETag validation, `ListParts` без `ListMultipartUploads`, отсутствие product object-count cap и отсутствие cross-store payload references; ObjectStore backup tests подтверждают closed exact-store metadata admission, short paginated SQLite reads без pinned WAL, concurrent writes после enumeration, cleanup serialization, отсутствие pins/local generation pointer и potentially long atomic metadata replacement при restore;
- privileged integration tests одних и тех же release bytes на каждом supported host;
- crash/restart tests в ключевых filesystem commit points, включая перевод `running` Operations/Backups и non-active running Deployments в `interrupted`, atomic active-pointer/success status, очистку `backups/work`, direct containers/storage enumeration/unmount/delete всех persistent container records/writable layers, empty container store assertion, teardown/recreate `/run/platformd/containers`, отсутствие runtime adoption и reuse только persistent image cache;
- disk-pressure tests отдельно для bytes/inodes подтверждают single-`st_dev` installation check, entry thresholds 90/95/97, exit hysteresis 88/93/95, pre-operation checks, cached active-pointer reconcile при critical, serialized orphan ObjectStore cleanup, reserve-file release/recreate, automatic freeze/unfreeze без durable emergency state и dirty-write limitation;
- cgroup tests подтверждают `DelegateSubgroup=control`, empty inner unit cgroup, отдельные workload leaves, conmon в `control`, рекурсивное завершение unit, unlimited CPU/memory defaults, exact optional hard limits, отсутствие global capacity-sum rejection и visible overcommit warning;
- malformed/fuzz Registry, S3, SigV4, DNS и proxy inputs;
- real Cloudflare test zone для Full (strict), Access JWT, WebSocket и registry push;
- browser E2E для projects, deploy, logs, terminals, data browsers, backup/restore и tokens;
- REST/MCP integration tests для arbitrary PostgreSQL SQL, project boundary и unbound-admin root exec с stdout/stderr, timeout и process-tree kill; они также подтверждают отсутствие token-authenticated container/server PTY и attach/resize endpoints; MCP transport tests подтверждают initial version negotiation без protocol header, required `serverInfo`, exact subsequent protocol header, dual-type `Accept`, `initialize`/`notifications/initialized`, POST-only stateless JSON, `202` notification response, отсутствие session ID/SSE/resume state и `405` для GET/DELETE; Access tests подтверждают JWKS refresh singleflight/cooldown/bounded negative cache; console-auth tests подтверждают single Argon2 verification, fixed failed-attempt delay, global in-memory cooldown/reset и отсутствие persisted/per-subject/per-IP counters;
- watcher integration test подтверждает polling только tag references, отсутствие polling для digest references, stale-result rejection после config change, automatic retry registry/pull errors без остановки active container и отсутствие automatic retry failed candidate той же пары `(serviceConfigHash, digest)` даже после daemon restart; новый digest/config и explicit Redeploy снимают блок;
- image compatibility tests с OCI image/index и Docker schema 2;
- network isolation и internal DNS tests;
- domain binding tests подтверждают отсутствие synthetic route ID/dangling route, global hostname uniqueness, SAN validation и atomic move между Services только при access к обоим projects;
- deployment tests подтверждают stop-first/no-overlap, simplified HTTP/process readiness, restart old при failure, immutable config snapshot, fixed restart enabled Service, stop/unpublish при disable, fresh Deployment при re-enable и rollback через копирование snapshot с pin exact digest;
- log tests подтверждают conmon native size rotation/reopen, запрет platformd rename/truncate active file, journald-only platform logs, grouping по Deployment/resource/job IDs и доступность history после recreation container с новым runtime ID;
- terminal browser tests подтверждают один lazy-loaded Ghostty-Web renderer для host/container, binary PTY I/O, bounded resize, copy/paste, hidden-pane refit, Access/Origin rejection, passphrase gate host shell и закрытие backend process group при disconnect;
- update tests подтверждают idle-only admission, `409 platform_busy` без cancellation, non-blocking reads/log streams, запрет новых mutations после gate, old-process graceful stop без duplicate teardown, cleanup строго до migration, rollback direct previous binary только при exact previous `PRAGMA user_version`, отказ rollback после commit, signed current-slot manifest validation и forward install previous binary после commit без открытия SQLite, отсутствие duplicate release metadata в SQLite, полный workload downtime, recreation runtime dir, reuse populated image cache после container-record purge, incompatible-cache fallback, missing image digest, crash до/после atomic `current` switch и migration commit, отсутствие pre-update SQLite backup и automatic deletion previous symlink/slot только после local/systemd readiness;
- release-manifest tests используют RFC 8785 canonical bytes, отклоняют malformed/duplicate JSON keys, signature/hash/size/Linux/amd64/`supportedFrom` mismatch, reinstall/downgrade и подтверждают automatic cache wipe без compatibility metadata;
- audit tests подтверждают отсутствие sensitive payloads/high-frequency protocol events и bounded automatic deletion AuditEvent старше 7 дней;
- managed-image selection tests подтверждают stateless paginated official tag listing, manual tag fallback, host-architecture digest resolution, отсутствие database catalog/defaults, pin exact digest и explicit failure вместо floating fallback при missing digest; engine profile tests на representative pre-18/18+ PostgreSQL и Redis tags проверяют fixed mount/config paths, hidden PostgreSQL bootstrap superuser, non-superuser owner auth/`SELECT 1`, Redis config readability после privilege drop и `AUTH`/`PING`;
- Change-version integration test без remote target: любое изменение digest, включая moved same tag, создаёт new volume; direct PostgreSQL stream, Redis BGSAVE status + replaced-inode proof + stable-FD RDB copy, единственный active volume pointer, отсутствие durable maintenance state, immediate old-volume deletion и orphan cleanup при crash до/после pointer switch;
- ordinary Volume tests подтверждают single-Service ownership, automatic owner inference только для numeric `uid:gid`, one-time numeric UID/GID initialization, отсутствие recursive deploy-time chown, запрет owner change после появления data и delete protection current/active references.

Release acceptance scenario:

1. На чистой supported VPS один файл выполняет `platformd init` без package manager.
2. Admin входит через Cloudflare Access.
3. Создаёт project и service из remote image.
4. Service доступен по domain через Cloudflare и видит соседний service по internal DNS.
5. Новый image digest автоматически приводит к deployment.
6. UI показывает logs и открывает container PTY.
7. Root PTY в UI требует console passphrase; unbound `admin` token выполняет bounded non-interactive root command через REST/MCP без passphrase.
8. Embedded registry принимает push/pull, public pull policy работает, а admin UI просматривает repositories/images и безопасно удаляет tag, image либо целый repository.
9. Embedded private S3 работает из service; optional public hostname принимает valid presigned URL и отклоняет anonymous request.
10. PostgreSQL/Redis создаются и доступны по internal DNS; UI имеет bounded data browsers, а PostgreSQL arbitrary SQL работает через UI, REST и MCP `admin`.
11. PostgreSQL, Redis, Registry и каждый ObjectStore независимо включают 5-field UTC cron/retention, создают encrypted generations и выполняют individual restore; disabled policy не создаёт scheduled backups.
12. Ordinary daemon restart и reboot останавливают workloads, полностью пересоздают `/run/platformd/containers` и все networks/containers из SQLite, переиспользуя только cached exact images.
13. Signed self-update использует тот же clean-runtime startup, переиспользует cached exact images и проходит schema migration; digest, отсутствующий в cache и registry, оставляет resource stopped, но admin UI доступна.
14. PostgreSQL и Redis on demand показывают tags official Docker repository и принимают manual tag; explicit image change без remote target всегда переносит data в новый volume, pin-ит exact host digest, сохраняет hostname и имеет ожидаемый downtime.
15. На новой VPS той же architecture `platformd init --restore` импортирует latest control generation до запуска рабочей admin UI, затем автоматически восстанавливается latest complete generation каждого resource; resource без backup и ordinary application volumes создаются пустыми без confirmation, а попытка cross-architecture restore отклоняется до изменения local state.

## 33. Явные non-goals v1

- multi-node и high availability;
- Dockerfile/Git builds;
- user-defined cron/one-shot scheduled service workloads; internal backup/maintenance scheduler остаётся;
- Kubernetes/Compose compatibility;
- multiple replicas/autoscaling;
- zero-downtime service deployment;
- arbitrary TCP/UDP public ingress;
- Cloudflare API/DNS/firewall synchronization;
- local RBAC поверх Cloudflare Access;
- hostile multi-tenancy;
- PostgreSQL PITR/physical backup/replication;
- Redis Cluster/Sentinel/AOF backup;
- anonymous/public-read S3, ACL/IAM/Object Lock;
- backup/restore ordinary application volumes;
- complete OCI Distribution и S3 protocol surface;
- tamper-proof audit;
- zero-downtime или unattended self-update;
- automatic/unattended, zero-downtime или in-place managed-database upgrades;

## 34. Принятые архитектурные решения

| ID | Решение |
|---|---|
| V2-01 | Single-file distribution использует appended bounded self-extracting ZIP; binary, signed release manifest и private runtime helpers публикуются вместе в одном immutable release slot |
| V2-02 | Go platformd импортирует pinned libpod напрямую; Podman API socket отсутствует |
| V2-03 | systemd управляет только platformd; platformd reconciles containers |
| V2-04 | Только готовые OCI images |
| V2-05 | Отдельная Netavark project network с отключённым plugin DNS; internal DNS встроен в platformd |
| V2-06 | Одна replica; каждый deployment использует stop-first с downtime независимо от volumes |
| V2-07 | Built-in HTTPS proxy на 443, без Caddy |
| V2-08 | Provider firewall допускает только Cloudflare; Access JWT проверяется origin |
| V2-09 | React/shadcn/Tailwind 4/tailwindcss-bun-plugin/Bun.build UI |
| V2-10 | Embedded minimal OCI Registry и private S3 внутри platformd |
| V2-11 | Registry не вводит собственный Cloudflare layer limit |
| V2-12 | PostgreSQL pg_dump и Redis RDB backups в remote S3, без PITR |
| V2-13 | PostgreSQL, Redis и S3 имеют bounded data browsers; PostgreSQL дополнительно предоставляет arbitrary SQL через UI/REST/MCP |
| V2-14 | Access принимает только RS256; issuer и exact JWKS URL выводятся из validated team domain и не настраиваются независимо |
| V2-15 | MCP и REST используют static tokens с role `read/admin` и optional exact project boundary |
| V2-16 | Interactive host root PTY требует init-only passphrase; unbound `admin` token выполняет non-interactive root exec без неё |
| V2-17 | Embedded S3 слушает только gateway IP соответствующей project bridge; gateway sidecar отсутствует |
| V2-18 | Один local master key является root для domain-separated local/backup keys; для восстановления нужна его копия вне VPS |
| V2-19 | SQLite — единственный authoritative product state; libpod DB является process-local disposable state под `/run`, а persistent containers/storage container records purged при каждом startup до fresh Runtime |
| V2-20 | S3 может иметь public Cloudflare hostname, но objects доступны только по SigV4/presigned URL |
| V2-21 | Self-update допускается только при idle execution state и ничего не отменяет; target сначала purges runtime/container records, затем commit-ит migration; rollback разрешён до commit, а после commit previous binary может только установить strictly newer signed forward fix |
| V2-22 | Embedded database catalog/defaults отсутствуют; UI stateless запрашивает tags official PostgreSQL/Redis repositories и допускает manual tag input |
| V2-23 | Любое explicit изменение managed DB digest всегда создаёт new volume и использует in-memory maintenance gate, direct data transfer и один durable active volume pointer; old volume удаляется сразу после switch, remote backup не обязателен и downtime принимается |
| V2-24 | Managed database хранит display tag и exact host digest; floating execution отсутствует, а missing digest не заменяется молча новым target того же tag |
| V2-25 | User-defined cron/scheduled service jobs отсутствуют; остаётся только internal maintenance scheduler |
| V2-26 | Каждый daemon startup через containers/storage API удаляет все persistent container records/writable layers, пересоздаёт `/run/platformd/containers` и runtime objects из SQLite; remaining graphroot используется только как best-effort image cache |
| V2-27 | Backup generations/policies/retention независимы; один worker без queue вычисляет due UTC cron из policies/history, busy manual request получает `409`, Registry/ObjectStore backup pins отсутствуют |
| V2-28 | Redis использует только RDB, без AOF; target RPO 5 minutes, actual RPO равен age последнего successful RDB |
| V2-29 | Full restore на новой VPS после потери старой поддерживается только на той же CPU architecture |
| V2-30 | Internet-facing Registry/S3/proxy остаются в root process; риск принят для v1 |
| V2-31 | S3 использует один отдельно не сохраняемый HKDF-derived key на ObjectStore и XChaCha20-Poly1305 chunks; online key rotation отсутствует |
| V2-32 | Image watcher использует polling; registry/pull errors retry-ятся, а failed candidate pair не повторяется автоматически по существующему Deployment history до нового digest/config либо manual Redeploy |
| V2-33 | Все non-S3 working backup keys HKDF-derived из master key и отдельно не сохраняются; online master-key rotation отсутствует |
| V2-34 | `init --restore` импортирует latest control generation до запуска admin UI, затем независимо latest complete generation каждого resource; отсутствующий backup и ordinary application volumes автоматически создаются empty без дополнительного confirmation |
| V2-35 | Unbound `admin` API token эквивалентен root credential и открывает REST/MCP `server_exec` |
| V2-36 | Один ObjectStore содержит ровно один immutable bucket; bucket lifecycle существует только в platform UI/API |
| V2-37 | Service readiness — optional HTTP GET либо fixed process startup grace; TCP/exec/threshold modes отсутствуют |
| V2-38 | Backup проверяется immediate remote read-back; отдельного scheduled restore-check нет |
| V2-39 | Backup target сохраняется только после stateless PUT/HEAD/GET/LIST/DELETE probe; capability state отсутствует, а target mutation запрещена во время любого remote action |
| V2-40 | In-memory dirty flag очищается до control snapshot, не теряет concurrent mutation и участвует в global oldest-due selection; вся control generation AEAD-encrypted, remote хранит ровно одну complete generation без resource payloads |
| V2-41 | External release manifest минимален и signed; binary проверяется по size/SHA-256, а helper hashes и parseable self-extracting bundle находятся только внутри binary |
| V2-42 | Registry blobs физически принадлежат одному repository; cross-repository dedup, blob links и global reachability graph отсутствуют |
| V2-43 | Каждый ObjectStore имеет отдельный payload/multipart directory; cross-store payload references отсутствуют |
| V2-44 | Container logs принадлежат Deployment/resource/job ID; disposable runtime container ID в log path не используется |
| V2-45 | Operation является только observational progress record; durable queue/resume/phase state отсутствует, а running после restart становится interrupted |
| V2-46 | Application domain — child binding Service, keyed hostname без synthetic ID/dangling state; explicit move atomically меняет target Service |
| V2-47 | Один `build.lock.json` фиксирует exact Linux/amd64 toolchain, helper source/flags и полный direct/transitive host SONAME allowlist; Go deps остаются только в go.mod/go.sum, frontend deps — в bun.lock, output hashes — в bundle manifest |
| V2-48 | Registry UI/API просматривает repositories/images и удаляет tag, manifest либо repository; blobs удаляются только repository deletion или explicit cleanup |
| V2-49 | MCP использует stateless JSON-response subset Streamable HTTP с exact 2025-11-25 initialize/serverInfo/header/Accept contracts, `GET/DELETE=405`, без session ID, SSE, server notifications и resumability |
| V2-50 | Remote backup catalog не кэшируется в SQLite; UI/restore выполняют bounded paginated LIST exact prefix и читают self-describing manifests |
| V2-51 | CPU/memory limits optional и default unlimited; hard limits применяются только по explicit user setting, global sum/admission guarantee отсутствует |
| V2-52 | Disk pressure имеет statfs-derived low/critical/emergency 90/95/97 с hysteresis 88/93/95, reserve-file release и automatic freeze/unfreeze без durable state/manual resume |
| V2-53 | Service имеет только `enabled` и image reference: tag автоматически poll-ится, digest pinned, а enabled long-running process перезапускается с fixed in-memory backoff; watch/restart/mode settings отсутствуют |
| V2-54 | Отдельной Service config revision entity нет: Service хранит mutable desired config, Deployment — immutable launch snapshot и digest; rollback копирует snapshot в Service, pin-ит digest и создаёт новый Deployment |
| V2-55 | Console passphrase использует одну Argon2 verification, fixed delay и global in-memory cooldown; persisted/per-subject/per-IP counters отсутствуют |
| V2-56 | Interactive container/server PTY используют один Ghostty-Web renderer и binary WebSocket transport только через Access-authenticated admin UI; API/MCP сохраняют лишь bounded non-interactive execution surfaces |
| V2-57 | ObjectStore использует current metadata rows и immutable payloads; backup кратко закрывает exact-store metadata admission и читает short pages без pinned WAL, затем writes продолжаются при cleanup exclusion; generation pointer/pins отсутствуют |
| V2-58 | Backup schedule — только validated 5-field Unix cron в UTC; seconds, macros, names и configurable timezone отсутствуют |
| V2-59 | `init` не хранит bootstrap phase/marker или подтверждение сохранения master key; interrupted init переиспользует key/SQLite, но может повторить prompt и шаги |
| V2-60 | Secret/RegistryCredential payload immutable; RegistryCredential принадлежит одному repository, rotation создаёт новый ID, а rollback с отсутствующей historical dependency завершается явно |
| V2-61 | systemd использует `DelegateSubgroup=control`; platformd/conmon и каждый workload находятся в разных cgroup leaves |
| V2-62 | Self-update manifest подписывает RFC 8785 canonical JSON одним неизменным v1 Ed25519 key и разрешает только strictly newer SemVer |
| V2-63 | AuditEvent автоматически удаляется через 7 дней; configurable retention/export отсутствуют |
| V2-64 | Registry cross-repository mount не поддерживается, но возвращает ordinary destination upload session для стандартного client fallback |
| V2-65 | Один Linux/amd64 artifact тестируется byte-for-byte на Ubuntu 24.04 и Debian 13; private glibc/ELF loader/shared libraries не поставляются, а полный host dependency graph проверяется против SONAME allowlist build lock |
| V2-66 | Restore import atomically сохраняет supplied working BackupTarget поверх historical target/credentials, использованных только для discovery/decryption control snapshot |
| V2-67 | Повторный ordinary `init` не меняет complete product config, но idempotently repair-ит symlink/unit и доводит service до local HTTPS readiness |
| V2-68 | Redis backup подтверждает новый successful BGSAVE через persistence status и replaced inode, затем stream-ит один stable FD |
| V2-69 | ObjectStore multipart temporary data зашифрованы тем же store key в отдельном AEAD domain; completion re-encrypt-ит final chunks и вычисляет plaintext SHA-256 ETag |
| V2-70 | Все managed data roots находятся на одном filesystem; init/startup отклоняет `st_dev` mismatch, а disk pressure допускает cached reconcile active pointers при critical |
| V2-71 | Managed PostgreSQL/Redis имеют fixed tested runtime profiles; PostgreSQL выдаёт отдельную non-superuser owner role, а Redis запускается с generated RDB-only config |

## 35. Нормативные внешние контракты

- [OCI Image Specification](https://github.com/opencontainers/image-spec)
- [OCI Distribution Specification](https://github.com/opencontainers/distribution-spec)
- [Podman/libpod](https://github.com/containers/podman)
- [Podman networking and DNS](https://docs.podman.io/en/stable/markdown/podman-network.1.html)
- [Cloudflare Full (strict)](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/)
- [Cloudflare Access JWT validation](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/)
- [Amazon S3 API](https://docs.aws.amazon.com/AmazonS3/latest/API/Welcome.html) и [Signature Version 4](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html), только заявленный §21 subset
- [tailwindcss-bun-plugin](https://github.com/iivankin/tailwindcss-bun-plugin)
- [Model Context Protocol](https://modelcontextprotocol.io/specification/2025-11-25)
- [Docker Official Image: PostgreSQL](https://github.com/docker-library/postgres)
- [Docker Official Image: Redis](https://github.com/redis/docker-library-redis)

`build.lock.json` §3.1 фиксирует exact external protocol revisions, helpers/toolchain и supported host contract; `go.mod`/`go.sum` фиксируют Go dependencies. Moving links выше являются index/reference, но implementation conformance проверяется против locked revisions. При конфликте implementation с заявленным subset внешнего протокола implementation исправляется либо capability удаляется из UI/docs; скрытая частичная совместимость запрещена.
