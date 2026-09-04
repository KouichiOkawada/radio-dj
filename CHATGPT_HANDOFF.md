# radio-dj Windows 化：ChatGPT 引き継ぎ資料

## Latest handoff — 2026-09-04 09:00 JST（最優先で読む）

- Repository: `C:\AI_TOOL\radio-dj`
- Branch: `feature/news-radio`
- Runtime: Windows native only（Docker / WSL は使わない）
- UI: `http://127.0.0.1:7710/`
- Icecast stream: `http://127.0.0.1:7702/stream.mp3`
- Active executable: `C:\AI_TOOL\radio-dj\radio-dj-new.exe serve`
- Config: `C:\Users\pesu1\.radio-dj\config.json`（APIキーをログや回答へ出さないこと）
- Music: `C:\Radio\music`; provider is OpenAI `gpt-4.1-mini`; Japanese Edge TTS.

### この引き継ぎで完了した安定化

1. モード変更は即時に曲を切らず、現在の音声セグメント終了時に適用する。
   `/now-playing` は `mode` と `pending_mode` を分け、UIも「切替予約」を表示する。
2. NEWS の準備完了を検知して再生中の曲を100msで切る処理を撤去した。
   実機で News Continuous のフォールバック曲が最後まで流れ、その直後にニュースへ移ることを確認済み。
3. RSS予約は「生成時」ではなく、ニュース音声の再生成功後だけ `news-seen.json` に記録する。
   失敗・破棄・モード変更時は予約を候補へ戻す。
4. ニュースREADYキューに番組枠、生成時刻、失効時刻を持たせた。別時間枠が先頭にあっても一致枠を探索し、期限切れを廃棄する。番組枠消化は `program-slots.json` に永続化。
5. ラジオのAI DJは曲の再生中に非同期で原稿/TTSを準備し、2〜4曲ごとの安全な曲間にだけ入れる。間に合わなければ無音にせず次の音楽へ進む。固定700msの曲中オーバーレイと汎用midrollは撤去。
6. News Continuous の非定時コピーは1記事ごとに約3分の `NewsCommentary`、定時ラジオ枠は短い `NewsBriefComment` を生成する。両方とも既存MP3のループBGMをミックスする。LLM HTTPには90秒timeoutを設定。
7. ニュース収集は再生と独立したgoroutineで継続。候補Storeは取得時刻も保持し、48時間TTLと正規化タイトル重複排除を行う。OGP画像取得も最大3並列・1回20記事で非同期化し、TTS/再生を待たせない。
8. UIはPC向け3カラム、明示的NOW/NEXT、ニュースカード、AI DJ発話、音量、モード/READY、折りたたみニュース設定を一画面に整理。API失敗はtoast表示し、リクエスト失敗時に入力を消さない。SSEは1本。外部DiceBear依存を廃止し `/dj-avatar.svg` を同梱。HTMLとservice workerは再検証されるキャッシュ設定。
9. タムラ製作所は固定監視から外し、`news_exclude_terms: ["タムラ製作所"]` で記事自体も候補から除外した。`watch_symbols` は空。
10. 株式記事だけ、記事に明示された証券コード、またはJ-Quants銘柄マスターの正式会社名が本文に厳密一致した会社を対象にする。無関係な固定銘柄は送らない。取得可能な昨日以前の直近5終値と最新財務開示をAIへ渡し、「上昇要因・下落要因・確認点」の条件付き見通しを話す。断定的予測・投資助言は禁止。Free planは12週間遅延しうるため、その可能性を明示する。

### 2026-09-04の検証結果

- `gofmt` 済み、`git diff --check` 問題なし。
- `go test ./...` 全成功。
- `go build -o radio-dj-next.exe .` 成功。
- 実UIをブラウザで確認：NOW/NEXT、ニュース、ローカルDJアバター、再生音量、モード、READY、transportが表示。
- 実ランタイム：`mode=news`、フォールバック音楽中にREADYが蓄積し、曲を途中で切らず終了後に `current.type=news` へ遷移。
- BGMミックスの既存実ログ：bulletin / AI commentary とも `BGM mixed ... at 15%`。

### 次に行うこと（未完）

- News Continuousで約3時間動作し、READY補充、ニュース+BGM、AIコメント+BGM、記事切替、J-Quants記事相関をログ確認した。11:49頃にGoのpanic/終了ログを残さずプロセスが消え、子ffmpegだけbroken pipeになったため、外部終了か未捕捉終了かは未確定。11:51に再起動済み。次回はプロセス終了コードを監視できるランチャーで再現性を確認する。
- `gpt-4.1-mini` の実出力で、会社一致した株式記事が「昨日以前の推移＋条件付き見通し」になっているか音声/ログを確認する。APIプランの遅延データを「昨日の実勢」と誤称しないこと。
- J-Quants銘柄マスターがページング応答になるプランの場合、正式会社名照合が先頭ページだけに限定されないか実レスポンスで確認する。
- 設定/診断をUIから編集する大規模機能までは今回実装していない。安定性を崩す追加改造より上記耐久確認を優先する。
- 古い本文の `Latest handoff — 2026-09-03` やOllama記述は履歴として残っているが、現在値はこの2026-09-04節を正とする。

## Latest handoff — 2026-09-03

- Branch: `feature/news-radio`; GitHub was fetched through `81e292b`, then
  `dead1ef fix: start radio music without waiting for Ollama` was added and pushed.
- `internal/radio/news_preload.go` prepares RSS → image → Japanese TTS →
  existing-library BGM mix → grounded DJ reaction in a background READY queue.
  `/now-playing` exposes `news_ready`, `news_ready_count`, and `news_state`.
- Windows is currently running `radio-dj-new.exe serve`, Icecast, and ffmpeg.
  UI: `http://127.0.0.1:7710`; stream: `/stream.mp3`.
- Latest verification: radio mode immediately selected a local track;
  `http://127.0.0.1:7702/stream.mp3` returned HTTP 200 and `ffprobe` reported MP3.
- Do not wait for `qwen3.5:4b` / Ollama before the first music batch.
- `news_state` was `loading` with zero READY items because `news-seen.json`
  had consumed the available fresh 72-hour RSS items. Radio must keep playing
  music; do not delete user music or relabel stale news as fresh.
- Windows Icecast recovery: `9c4812e` waits after killing a dead master ffmpeg,
  allowing the reopen path to run instead of looping on Broken pipe.
- News preloading now targets four complete READY breaks continuously. Stories
  are reserved in memory while rendering and persisted to `news-seen.json` only
  when their factual segment actually reaches air. Discarded mode-prefetches are
  released. The post-news AI-DJ comment is also mixed with the configured,
  looping news BGM before entering READY.

## 目的

Windows PC 上で、ローカル音楽ファイルを連続再生しながら、AI DJ の日本語コメントと RSS ニュースを挟む個人用ラジオを作る。

目標の流れ：

`音楽 → AI DJ → ニュース → 音楽 → …`

Docker と WSL は使用しない。対象リポジトリは `C:\AI_TOOL\radio-dj`。

## 現在の実行構成

| 項目 | 現在値 |
| --- | --- |
| アプリ | `C:\AI_TOOL\radio-dj\radio-dj.exe` |
| 作業ブランチ | `feature/news-radio` |
| 音楽フォルダ | `C:\Radio\music` |
| アプリ設定 | `C:\Users\pesu1\.radio-dj\config.json` |
| UI | `http://127.0.0.1:7710` |
| ストリーム | `http://127.0.0.1:7702/stream.mp3` |
| LLM | Ollama `http://127.0.0.1:11434/v1` / `qwen3.5:4b` |
| 日本語 TTS | `edge-tts` / `ja-JP-NanamiNeural` |
| 配信サーバー | Icecast 2.5 (`C:\Program Files\Icecast`) |
| ffmpeg | `C:\Users\pesu1\Desktop\ffmpeg-7.0.2-essentials_build\bin\ffmpeg.exe` |
| RSS | NHK ニュース `https://www3.nhk.or.jp/rss/news/cat0.xml` |

## 確認済みの事実

- `C:\Radio\music` に MP3 が存在し、タグも読み取れている。
- Ollama のモデル `qwen3.5:4b` はローカルに存在し、OpenAI 互換エンドポイントへ接続できる。
- Icecast、radio-dj、ffmpeg が実行中。
- `curl http://127.0.0.1:7702/stream.mp3` は HTTP 200 で MP3 データを返し、`ffprobe` で MP3 と判定済み。
- `/now-playing` で曲情報を取得できる。
- RSS は HTTP 200 で取得できる。
- Edge TTS は日本語 MP3 の生成を確認済み。

## 実装済みの変更

1. Windows ネイティブ対応
   - Windows 用タスクスケジューラの install/uninstall 実装。
   - Windows のホーム・実行ファイルパス対応。
   - Icecast / ffmpeg の Windows 検出。
   - Windows は Go の `ExtraFiles` が使えないため、ffmpeg の stdin へ PCM を送る方式に変更。

2. Ollama 対応
   - `llm_provider: ollama` では API キーなしで DJ を有効化。
   - 空の Authorization ヘッダーを送らない。

3. 日本語対応
   - `internal/i18n/prompts/ja.json` と `internal/i18n/skills/ja/` を追加。
   - 設定の `language: ja` で日本語プロンプトを使用。

4. RSS ニュース
   - `internal/news/news.go` を追加。
   - RSS / Atom の title / description だけを読み、出典名を付けて読み上げ原稿を決定的に生成する。
   - ニュース本文を LLM に要約させないため、ニュース内容を捏造する経路はない。

5. 日本語 TTS
   - Windows では `edge-tts` をシェル経由でなく argv で直接起動し、日本語引数の文字化け・分割を回避。

## 未解決の重要事項

### 1. AI DJ の安定したオンエア挿入

`qwen3.5:4b` は reasoning に多くのトークンを使う。構成 JSON の生成に時間がかかり、初回に Icecast が音源を受け取れない問題があった。

現在は `internal/radio/serve.go` で Ollama 使用時だけ、起動直後の構成プランナーを回避して音楽を即時開始している。これにより音楽配信は安定したが、AI DJ の曲紹介をストリーム上で安定的に確認する作業が残っている。

推奨方針：

- 音楽の先読みキューと DJ 原稿生成を別 goroutine に分離する。
- 曲の開始を LLM 応答待ちにしない。
- DJ 原稿が準備できた場合だけ次の曲間へ挿入し、失敗時は無音ではなく音楽を続ける。
- 4B モデルの structured JSON 生成を避け、短い曲紹介テキストだけを生成する設計も検討する。

### 2. ニュースの重複生成

現在の producer ループでは `news_every` 判定が同じ track count で複数回走る可能性がある。最後に放送したニュース時点を `stateDir` に記録し、同一 RSS エントリ・同一 tanda で繰り返さないようにする必要がある。

### 3. Windows のダッキング

Unix 版は二つの ffmpeg pipe により音楽をダッキングして DJ 音声を重ねる。Windows は Go の `ExtraFiles` 非対応のため単一 stdin PCM 方式であり、現状は曲間挿入を優先している。リアルタイムの曲中ダッキングは未完成。

## 直近コミット

```text
8987f46 fix: start Ollama stations with music immediately
599ed6a fix: allocate Ollama reasoning budget for DJ speech
6d93b68 fix: preserve Japanese Edge TTS arguments on Windows
e1b7d40 feat: add attributed RSS news bulletins
175c173 fix: stream audio through stdin on Windows
d19ec50 feat: add Japanese DJ prompts and Windows Icecast paths
d1b8d0c fix: support local Ollama on Windows
0f94961 feat: add native Windows runtime support
```

## すぐ使う確認コマンド（PowerShell）

```powershell
Get-Process radio-dj,icecast,ffmpeg -ErrorAction SilentlyContinue
Invoke-RestMethod http://127.0.0.1:7710/now-playing
curl.exe --max-time 5 --range 0-4095 -o $env:TEMP\stream.mp3 http://127.0.0.1:7702/stream.mp3
ffprobe -v error -show_entries format=format_name -of default=noprint_wrappers=1 $env:TEMP\stream.mp3
Get-Content C:\Users\pesu1\.radio-dj\radio-dj.err.log -Tail 100
```

## 依頼文（他の ChatGPT へそのまま貼る用）

```text
C:\AI_TOOL\radio-dj の Windows ネイティブ AI ラジオ開発を引き継いでください。
まず CHATGPT_HANDOFF.md を読み、Git の現在ブランチ feature/news-radio と実行状態を確認してください。

音楽と RSS ニュースのストリームはすでに動いています。次の最優先事項は、Ollama qwen3.5:4b の応答待ちで音楽配信を止めずに、日本語 AI DJ の曲紹介を実際のストリームへ安定して挿入することです。

ニュースは title / description のみを出典付きで読む設計を維持し、LLM にニュース本文を創作・要約させないでください。
Docker / WSL は禁止です。Windows ネイティブで作業してください。
変更前後に go test ./... と実ストリームの HTTP 200 / ffprobe 確認を行い、Git commit を残してください。
```
