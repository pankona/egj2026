# CLAUDE.md

Ebitengine Game Jam 2026 への参加作品。テーマは **DISCONNECT（切断）**。
コードネームは `Rift`。Go + Ebitengine + WebAssembly。

## ゲーム概要

ドラッグで「方向と位置」を入力すると、指を離した瞬間にドラッグの中点を中心に**固定長の光の衝撃波**が左右両側へズバッと伸びる。3 連発した直線でポリゴン状に囲んだ暗領域は一気に光で塗りつぶされる（claim）。敵は光の縁を侵食して取り戻しに来る。各ステージの光カバー率しきい値を制限時間内に達成すると次へ。最終 10 ステージは巨大ボス 1 体を光で封じ込めるとクリア。

- 解像度: 640x480、セル 8px の 80x60 グリッド (`CellSize`, `GridWidth`, `GridHeight`)
- 入力: マウスドラッグ／タッチドラッグ（タッチ優先、`pointerPos` 参照）。フリックでも発動するよう、確定に必要なドラッグ距離は `SlashMinLength` だけ
- 斬撃 (`fireSlash` → `updateSlashes` → `burnSegment` → `illuminate`): ドラッグの中点を中心に、方向ベクトル両側へ `SlashLength` の固定長で伸びる。`SlashRevealFrames` で全長まで展開し、`SlashGlowFrames` で残光が消える。刃の半径は `BladeRadius` セル。アクティブな斬撃は `Game.slashes []*Slash` で管理し、毎フレーム tip が進んだ分だけ grid に焼く
- 領域確定 (`claimEnclosure`): 斬撃が全長まで伸びた瞬間にプレイフィールド (常時壁の外周 1 セル内側) を 4-連結の暗領域に分解。**最大の連結成分を「外側」とみなし、それ以外の小さな領域を全部 claim** する。画面端は常時の壁 (Draw 側で 1 セル幅のフレームとして可視化) なので、斬撃が画面端と組み合わせて切り取った小領域も自然に claim 対象になる。内部の敵は除去
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

| 状態          | 入力                                                                          |
| ------------- | ----------------------------------------------------------------------------- |
| タイトル      | クリック / タップ / Space で開始                                              |
| プレイ中      | ドラッグで方向決定、離した瞬間に固定長の衝撃波が発動。3 発撃つとリロード待ち |
| クリア        | 自動で次ステージへ                                                            |
| タイムアップ  | クリック / Space で同ステージリトライ                                         |
| 全クリア      | クリック / Space でタイトルへ                                                 |

斬撃は `MaxStock` 発分のストック制。1 ドラッグ = 1 発消費、`stock` で残弾を持ち、HUD には残弾ピップ + (リロード中だけ表示される) リロードゲージを描画。リロードは「使い切り一括補充」: ストックが残っている間はゲージが進まず、`stock == 0` になった瞬間に `reloadProgress` が回り始め、`ReloadFrames` フレーム後に `MaxStock` 発が一気に補充される。短すぎるドラッグ (`SlashMinLength` 未満) は方向不安定とみなしストックを消費しない。

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

## 設計メモ: なぜ「直線の衝撃波」になったか

初期プロトはフリーハンドのドラッグで軌跡をそのまま光化する素直な作りだったが、テーマ DISCONNECT を体験させる手触りとして「線をなぞる」が弱かった。以下の設計判断を順に重ねて現行の衝撃波方式に着地している。

- **直線一閃**: フリーハンドを捨て、ドラッグの「向き」と「位置」だけを使う。1 操作 = 1 ストロークの儀式にすることで「ズバッと切る」感を出す
- **中点中心の対称展開**: 始点・終点を結ぶ線分の中点を中心に、方向ベクトル両側へ `SlashLength` の固定長で伸ばす。ドラッグ距離は方向の入力でしかなく、刃の長さに影響しない
- **少操作 = 大影響**: `SlashMinLength` を 12px まで下げ、フリックでも発動する。「ちょっと指を動かしただけで画面に大きな切れ込みが入る」衝撃波感が目的
- **即時展開 + 衝撃波の輪**: `SlashRevealFrames = 1` で全長を 1 フレームで描き、中点から白い輪を `Frame ≤ 4` の数フレームだけ広げて消す。`SlashGlowFrames` の残光と組み合わせて「ドンッ」のリズム
- **3 発ストック + リロード**: 3 本の直線で最小ポリゴン (三角形) が組める。撃ち切りリロードのリズムが「溜めて放つ」緊張感になる。`charge`/`MaxCharge`/`ChargeRecover` の旧チャージシステムは廃止
- **claim は連結成分 + 最大領域以外を取る**: 当初は「画面端から flood-fill して外側を確定 → 残りを claim」の単純実装だったが、画面端を「常時壁」にした時点で、起点をどこに置いても「リング状の起点エリア = 内側全域に届いて全部 outside」になってしまい claim できなくなった。現行は起点を持たず、暗領域を全部連結成分に分解 → 最大成分を「外側」、残りを全部 claim、と判定する。完全に閉じた多角形でも、画面端と組み合わせた小領域でも、同じロジックで扱える
- **画面端は常時の壁**: 最外周 1 セルを「あらかじめ引いてある線」として扱い、Draw 側で 1 セル幅のフレームを描画。`claimEnclosure` の連結成分判定はこの 1 セル分を壁としてスキップするので、画面端と斬撃で囲った領域も小領域として claim される
- **ドラッグ予告は二層**: ドラッグ中は (a) 始点〜現在ポインタの短い実線 (指先に追従する手触り) と (b) 中点を中心とした薄いフル長ゴースト (確定後の刃の予告) を重ねて描画する

将来「画面が物理的に裂ける」「闇のかけらが破片化して落ちる」などの別アイデアは別軸として保留中。まずはこの衝撃波体験の調整 (`SlashLength` の長さ、`ReloadFrames` のテンポ、ステージごとのストック数) を優先する。

## ジャム制約

- 期間内に作ったものだけが対象。前年作品 (`pankona/egj2025`) からのコピーは禁止。`Makefile` / wasm 周りの定型部分は **参考にして書き直す** 方針
- 1 週間（〜2026-06-28 前後）の 1 人開発。残り時間が短いので、まず最小プロト → 手触り → ステージ拡張の順を崩さない
- 動詞「切る／囲んで光を奪い返す」がテーマ DISCONNECT を体験させる軸。「ただ避けるアクション」に薄まらない調整を優先する
