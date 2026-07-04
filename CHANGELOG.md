# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and this project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- `kindle-cli ls` 서브커맨드: 연결된 기기의 documents/ 목록을 크기·cdetype
  태그(PDOC/EBOK)와 함께 표시. 파일명에 없는 제목/저자는 EXTH 메타데이터로
  보충하고, `--all`로 사이드카(.sdr)·숨김 파일까지 볼 수 있음.
- `curl … | sh` 원라이너 설치 스크립트(`install.sh`): 플랫폼 자동 감지,
  최신 릴리스 다운로드, 체크섬 검증 후 `~/.local/bin`에 설치.
  `KINDLE_CLI_VERSION`/`KINDLE_CLI_INSTALL_DIR`로 버전·위치 지정 가능.

## [0.0.1] - 2026-07-05

### Added
- Initial release.
- `kindle-cli`: EPUB → AZW3(KF8) 변환 후 USB로 연결된 Kindle에 MTP(gio/gvfs)로
  푸시하는 사이드로드 CLI. 변환은 순수 Go([leotaku/mobi](https://github.com/leotaku/mobi)
  KF8 writer + 자체 EPUB 리더)로 Calibre 등 외부 의존성 없이 동작.
- `cdetype`을 생성 시점에 `PDOC`(EXTH 501)으로 기록해 최신(2024+) Kindle에서
  커버가 표시됨; `--keep-ebok`으로 `EBOK` 유지 가능.
- 책 내부 하이퍼링크(각주 점프, 상호 참조)를 KF8 위치 링크
  (`kindle:pos:fid:…:off:…`)로 변환 — 기기에서 탭하면 실제로 점프함.
  대상 id가 없는 링크는 챕터 시작으로 폴백.
- 이미지(JPEG/PNG/GIF)는 원본 바이트 그대로 임베드 — 재인코딩 손실 없음,
  PNG 투명도 보존. Kindle이 못 그리는 변종(프로그레시브/CMYK JPEG 등)만
  베이스라인 JPEG으로 재인코딩.
- AZW3/MOBI 입력은 변환 없이 푸시. `cdetype`을 확인해 필요하면 사본에만
  `PDOC`으로 재태깅하고 원본은 수정하지 않음.
- 변환된 AZW3는 원본 EPUB 옆에 같은 이름으로 저장; `--out-dir`로 위치 변경.
- 메타데이터는 CLI 오버라이드 → OPF → `Title - Author.epub` 파일명 순으로 채움.
- 디렉터리 입력 시 `*.epub`/`*.azw3`를 함께 수집하고, 같은 이름의 EPUB이 있는
  AZW3는 건너뜀. 글롭 패턴은 셸이 확장하지 않아도 자체 확장.
- 배치 처리 시 파일별 실패 격리.
- `--no-push`, `--out-dir`, `--keep-ebok`, `--no-replace`, `--title`,
  `--author`, `--quiet` 옵션.

### Conversion notes
- 임베디드 폰트는 제거됨(기기 폰트 사용; 출력이 훨씬 작아짐).
