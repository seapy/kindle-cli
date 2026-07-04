# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and this project adheres to
[Semantic Versioning](https://semver.org/).

## [0.0.1] - 2026-07-05

### Added
- Initial release.
- `kindle-cli`: EPUB → AZW3(KF8) 변환 후 USB로 연결된 Kindle에 MTP(gio/gvfs)로
  푸시하는 사이드로드 CLI. 변환은 순수 Go([leotaku/mobi](https://github.com/leotaku/mobi)
  KF8 writer + 자체 EPUB 리더)로 Calibre 등 외부 의존성 없이 동작.
- `cdetype`을 생성 시점에 `PDOC`(EXTH 501)으로 기록해 최신(2024+) Kindle에서
  커버가 표시됨; `--keep-ebok`으로 `EBOK` 유지 가능.
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
- 책 내부 하이퍼링크(각주 점프)는 일반 텍스트로 평탄화됨.
- 이미지는 JPEG으로 재인코딩되며 투명 배경은 흰색으로 합성됨.
