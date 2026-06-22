# CLAUDE.md

Ebitengine Game Jam 2026 への参加作品。テーマは **DISCONNECT（切断）**。
コードネームは `Rift`。Go + Ebitengine + WebAssembly。

## ゲーム概要

ドラッグで「光の刃」を引いて闇のフィールドを切り開き、囲んだ領域を一気に光で塗りつぶす（claim）。敵は光の縁を侵食して取り戻しに来る。各ステージの光カバー率しきい値を制限時間内に達成すると次へ。最終 10 ステージは巨大ボス 1 体を光で封じ込めるとクリア。

- 解像度: 640x480、セル 8px の 80x60 グリッド (`CellSize`, `GridWidth`, `GridHeight`)
- 入力: マウスドラッグ／タッチドラッグ（タッチが優先、`pointerPos` 参照）
- 切る (`cutLine` → `illuminate`): 刃の半径は `BladeRadius` セル
- 領域確定 (`claimEnclosure`): ポインタを離した瞬間に外周→外側を flood-fill。閉じた暗領域を見つけたら現在の hue で塗り、内部の敵は除去
- 侵食 (`erodeAround`): 敵のまわりの光セルを暗くする。光の縁 (`WallLightThreshold` 未満の隣接セルを持つセル) は `EnemyEdgeBoost` 倍で削れる
- AI (`steerEnemy` / `findNearestLightEdge`): 敵は近傍の光の縁セルを `EnemyRetargetSec` 間隔でロックして寄ってくる

## 全体構成

```
main.go                  Game 本体 (Update/Draw/状態遷移/グリッド・敵・claim)
debug_mode_default.go    DEBUG 環境変数読み取り (非 wasm)
debug_mode_wasm.go       ?debug=1 URL クエリ読み取り (wasm)
devserver/main.go        dist/ を配信する開発用 HTTP サーバ (port 18081)
web/index.html           外側ページ。game.html を iframe で読み込み
web/game.html            wasm 実体を読み込む内側ページ
Makefile                 build / build-wasm / serve-wasm / devserver / fmt / release
```

`Stage` テーブル (`stages`) で `Enemies / EnemySpeed / WinThreshold / TimeLimit / Boss` を 1 行ずつ並べて難易度を作る。新規ステージ追加・調整はここを編集する。

`GameState` (`StateTitle / StatePlaying / StateCleared / StateGameOver / StateAllCleared`) の遷移は `Update` のスイッチで一元管理。クリア／ゲームオーバー時の `postClearCooldown` は次入力を受ける前のクッション。

`Game.grid [GridWidth][GridHeight]Cell` が描画の真実の入れ物。`Cell.Light` が 0..1、`R/G/B` がそのセルに塗られた hue 由来の色。`Draw` 側で `Light * (R/G/B)` を 8bit に量子化して `gridImg` に書き、`CellSize` 倍に拡大して描画。

## 操作

| 状態          | 入力                                    |
| ------------- | --------------------------------------- |
| タイトル      | クリック / タップ / Space で開始        |
| プレイ中      | ドラッグで光の刃。離した瞬間に claim 判定 |
| クリア        | 自動で次ステージへ                       |
| タイムアップ  | クリック / Space で同ステージリトライ     |
| 全クリア      | クリック / Space でタイトルへ             |

`charge` (`MaxCharge=900`) はドラッグ中に距離分消費され、押していないときに `ChargeRecover` ずつ回復。チャージが切れると刃が引けなくなる。

## ビルド・実行

```
make run            ネイティブ実行 (go run .)
make build          ネイティブバイナリを dist/rift に出力
make build-wasm     wasm + wasm_exec.js + web/ を dist/ にまとめる
make devserver      dist/ を localhost:18081 で配信
make serve-wasm     build-wasm → devserver
make release        wasm をビルドして rift-<short-sha>.zip を作る
make lint           GOOS=js GOARCH=wasm go vet ./...
make test           go test -v ./...
make fmt            goimports -w .
```

デバッグ表示 (FPS) は `DebugMode = true` のときだけ。
- ネイティブ: `DEBUG=1 go run .`
- wasm: `http://localhost:18081/?debug=1`

## コーディング規約

- パッケージは `main` 1 つ。ファイルを分けるよりは関数で切る方針。WASM 固有処理だけビルドタグで切り出す (`debug_mode_*.go`)
- 定数は `main.go` 冒頭のブロックに集約。チューニング対象は基本そこに足す
- 命名: グリッド座標は `cx, cy` / セルオフセットは `dx, dy` / ピクセル座標は `x, y` または `px, py` を踏襲
- コメントは「なぜ」だけ書く。`Stage`, `pointerPos`, `claimEnclosure` などにあるブロックコメントの粒度を参考に
- ゲーム挙動を変える前に、まず `stages` テーブルやチューニング定数で再現できないかを確認する

## ジャム制約

- 期間内に作ったものだけが対象。前年作品 (`pankona/egj2025`) からのコピーは禁止。`Makefile` / wasm 周りの定型部分は **参考にして書き直す** 方針
- 1 週間（〜2026-06-28 前後）の 1 人開発。残り時間が短いので、まず最小プロト → 手触り → ステージ拡張の順を崩さない
- 動詞「切る／囲んで光を奪い返す」がテーマ DISCONNECT を体験させる軸。「ただ避けるアクション」に薄まらない調整を優先する
