# CLAUDE.md

Ebitengine Game Jam 2026 への参加作品。テーマは **DISCONNECT（切断）**。
コードネームは `Rift`。Go + Ebitengine + WebAssembly。

## ゲーム概要

ドラッグで「方向と位置」を入力すると、指を離した瞬間にドラッグの中点を中心に**固定長の光の衝撃波**が左右両側へズバッと伸びる。3 連発した直線でポリゴン状に囲んだ暗領域は一気に光で塗りつぶされる（claim）。敵は光の縁を侵食しながら、複数体が連携して囲い返してくる。**ステージの敵を全滅させると次へ**。最終 10 ステージは巨大ボス 1 体を光で封じ込めるとクリア。

- 解像度: 640x480、セル 8px の 80x60 グリッド (`CellSize`, `GridWidth`, `GridHeight`)
- 入力: マウスドラッグ／タッチドラッグ（タッチ優先、`pointerPos` 参照）。フリックでも発動するよう、確定に必要なドラッグ距離は `SlashMinLength` だけ
- 斬撃 (`fireSlash` → `updateSlashes` → `burnSegment` → `illuminate`): ドラッグの中点を中心に、方向ベクトル両側へ `SlashLength` の固定長で伸びる。`SlashRevealFrames` で全長まで展開し、`SlashGlowFrames` で残光が消える。刃の半径は `BladeRadius` セル。アクティブな斬撃は `Game.slashes []*Slash` で管理し、毎フレーム tip が進んだ分だけ grid に焼く
- 領域確定 (`claimEnclosure`): 斬撃が全長まで伸びた瞬間にプレイフィールド (常時壁の外周 1 セル内側) を 4-連結の暗領域に分解。**画面端に接する成分は「外側」扱いで除外し、完全に閉じた領域だけを claim** する。縦 2 本などの「画面を分割するだけ」のストロークは何も取れず、3 本以上で多角形を組まないと光化されない。内部の敵は claim と同時に除去
- 侵食 (`erodeAround`): 敵のまわりの光セルを暗くする。光の縁 (`WallLightThreshold` 未満の隣接セルを持つセル) は `EnemyEdgeBoost` 倍で削れる。`EffectRadius = 0` の敵（チュートリアル用）は呼ばれても無効
- 結界 (`advanceBindPhase` → `bindEdgesNow` → `bindEnclosure`): `EnableBind` ステージでは全敵共通タイマー `bindPhase` が Roaming → Warning → Holding を周回。Holding 終了フレームで、静止中の敵ペアを暗線として描いた壁集合と既存の暗領域を合算し、明領域を連結成分に分解 → 画面端に接しない閉領域を暗化する。プレイヤー側 claim の鏡写し
- AI (`steerEnemy` / `findNearestLightEdge`): 敵は近傍の光の縁セルを `EnemyRetargetSec` 間隔でロックして寄ってくる。Anchoring 中（`bindPhase != 0`）は移動・侵食・retarget を全部スキップして頂点として固まる

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

`Stage` テーブル (`stages`) で `Enemies / EnemySpeed / TimeLimit / Boss / EnableBind / HarmlessEnemy` を 1 行ずつ並べて難易度を作る。新規ステージ追加・調整はここを編集する。`EnableBind` を立てると結界フェーズが解禁され、`HarmlessEnemy` を立てると `EffectRadius=0` の侵食しない敵が生える（ステージ 1 のチュートリアル用）。

`GameState` (`StateTitle / StatePlaying / StateCleared / StateGameOver / StateAllCleared`) の遷移は `Update` のスイッチで一元管理。クリア／ゲームオーバー時の `postClearCooldown` は次入力を受ける前のクッション。勝利条件は全ステージ共通で **`len(g.enemies) == 0`** の一本道。

`Game.grid [GridWidth][GridHeight]Cell` が描画の真実の入れ物。`Cell.Light` が 0..1、`R/G/B` がそのセルに塗られた hue 由来の色。`Draw` 側で `Light * (R/G/B)` を 8bit に量子化して `gridImg` に書き、`CellSize` 倍に拡大して描画。

敵の描画は球面ランバート反射モデル（`Draw` 内の敵ループ）。各敵について全画面のグリッドを線形 falloff で集計し、加重平均から「偏り度 (`asymmetry`)」を計算、`ambient + directional * max(0, n·L)` の合成で `stepPx` 刻みの小矩形格子として塗る。`totalLight == 0` の完全暗闇では描画スキップ。詳細は下の設計メモ参照。

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
make watch          air で wasm を再ビルド + devserver を再起動 (初回のみ install-tools)
make release        wasm をビルドして rift-<short-sha>.zip を作る
make lint           GOOS=js GOARCH=wasm go vet ./...
make test           go test -v ./...
make fmt            goimports -w .
```

デバッグ表示 (FPS) は `DebugMode = true` のときだけ。
- ネイティブ: `DEBUG=1 go run .`
- wasm: `http://localhost:18081/?debug=1`

**ネイティブビルド (`go build` / `make build` / `make run`) はこの開発環境では確認しない**。配布形態が wasm 限定で、かつ WSL の開発機には `libXxf86vm` 等の X11 ライブラリが入っていないため、`go build ./...` は毎回リンカエラーで落ちる（環境問題で本件と無関係）。動作検証は常に `GOOS=js GOARCH=wasm go vet ./...`（= `make lint`）と `go test ./...`（= `make test`）で済ませる。

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
- **claim は完全閉領域だけ**: 当初は「画面端から flood-fill して外側を確定 → 残りを claim」だったが、画面端を「常時壁」にしたら起点エリア = 内側全域につながって claim できなくなった。次に「暗領域を連結成分に分解 → 最大成分を外側 → 残りを claim」に変えたが、これだと縦 2 本引いただけで両端の細長い 2 領域が claim されて 50% 以上塗れてしまうチート気味のムーブが成立した。現行は **「画面端の内周 1 セルに接する成分は『外側』扱いで除外、画面端と縁を共有しない完全閉領域だけ claim する」**。直線 3 本で三角形を組む儀式が claim 成立の最小単位になっている
- **画面端は常時の壁**: 最外周 1 セルを「あらかじめ引いてある線」として扱い、Draw 側で 1 セル幅のフレームを描画。`claimEnclosure` の連結成分判定は内周 1 セルに接した時点で「外側」と判定する。画面端と斬撃を組み合わせて細長い領域を切り出しても、それは画面端に接しているので claim 対象外
- **ドラッグ予告は二層**: ドラッグ中は (a) 始点〜現在ポインタの短い実線 (指先に追従する手触り) と (b) 中点を中心とした薄いフル長ゴースト (確定後の刃の予告) を重ねて描画する

将来「画面が物理的に裂ける」「闇のかけらが破片化して落ちる」などの別アイデアは別軸として保留中。まずはこの衝撃波体験の調整 (`SlashLength` の長さ、`ReloadFrames` のテンポ、ステージごとのストック数) を優先する。

## 設計メモ: なぜ敵に「結界」を持たせたか

初期実装の敵は `erodeAround` で光の縁をジワジワ削るだけで、プレイヤーへの直接の脅威がなく、無視して claim を回しても勝てる消化試合になりがちだった。テーマ DISCONNECT の「光と闇のせめぎ合い」を儀式の対称として見せるため、敵側にもプレイヤー claim と鏡写しになる「結界」儀式を持たせている。

- **3 段サイクル**: `bindPhase` が Roaming (`0`) → Warning (`1`、脈動オーラの予告) → Holding (`2`、暗線が中点から両端へ伸びるアニメ) を周回。タイマーは全敵共通で、`BindRoamFrames` / `BindWarnFrames` / `BindHoldFrames` で各フェーズの長さを定義
- **頂点と暗線**: Anchoring 中（phase 1/2）は敵が一斉に静止して頂点になる。`bindEdgesNow` が距離 `BindRangeCells` 以内のペアを動的に列挙、`severedPairs` に登録されていない組だけが暗線として描画・暗化に寄与する
- **介入手段**: 静止中の敵に斬撃が当たれば撃破（`fireSlash` 内で `pointSegmentDistance` 判定、撃破した頂点を含むペアは自動で無効化）。完成中の暗線と斬撃の線分が `segmentsIntersect` で交差すれば、その組が `severedPairs` に登録されて bindEnclosure の壁ラスタライズから除外される
- **bindEnclosure**: claim の鏡写し。暗線をラスタライズした dark cells に既存の暗領域を足して壁集合とし、「明領域」を 4-連結成分に分解、`claimEnclosure` と同じく画面端に接しない閉領域だけを `Light=0` に落とす
- **Boss と敵 1 体ステージはスキップ**: 結界は最小 2 頂点が要るので、`advanceBindPhase` は `s.Boss || len(g.enemies) < 2` で phase 進行を止める。`severedPairs` も同時にクリア

## 設計メモ: なぜ勝利条件を「敵全滅」に揃えたか

初期は「光カバー率しきい値 (`WinThreshold`) を制限時間内に達成」が勝利条件だったが、ボスステージだけ別ロジック (`len(g.enemies) == 0`) が走っていて分岐が二重に走っていた。プレイヤー視点でも「カバー率を満たして勝ちが確定したのに、敵がまだ画面に残っている」状態が起きて、消化試合のような違和感が出ていた。

判定を一本化して「敵全滅でクリア」に統一。`Stage.WinThreshold` フィールドも削除。ステージ 1 は敵 0 では成立しないので、`HarmlessEnemy: true` の極低速（`EnemySpeed=0.20`）・`EffectRadius=0`（侵食しない）の 1 体に置き換え、「斬撃を引いて閉領域で claim する」基本ループを 30 秒で学ばせるチュートリアルに。光カバー率は HUD の進捗メータ（`Light xx%`）として残してあるが、勝敗には関わらない。

敵を倒す手段は (a) `claimEnclosure` で完全閉領域を作って中の敵を除去、(b) 結界の Anchoring 中に斬撃で頂点を撃破、の 2 ルート。Roaming 中の敵は斬撃で倒せないため、結界フェーズが「攻めのチャンス」になるリズムを意図的に作っている。

## 設計メモ: なぜ敵を球面ランバートで描くか

斬撃を引いてないステージ序盤は画面のほとんどが暗闇で、敵の位置が当てずっぽうになる課題があった。プレイヤーフィードバックでは「斬撃 = 光源」「敵の輪郭が光に当たって浮かび上がる」のメタファーがハマっていたので、敵の描画を「斬撃で塗られた光を集めて球体に当てるランバート反射」モデルに書き直した。Kage シェーダーは使っていない — 全部 CPU 計算で `vector.DrawFilledRect` を格子状に並べる方式。

- **ライト集計**: 敵ごとに `lightMaxDist`（= 100 セル、画面対角を覆う）範囲内のグリッドを全走査。線形 falloff (`1 - d/maxDist`) で `totalLight` / 加重方向ベクトル (`dirX`, `dirY`) / 加重色 (`litR/G/B`) / 加重距離 (`totalDistance`) / 最大単一寄与 (`maxContribution`) を集計
- **brightness の根拠**: `sqrt(total/N)` や正規化ではなく `maxContribution` を使う。これで「画面のどこかに光があれば必ず brightness > 0」が保証され、敵から離れた初撃でも敵が薄く浮かぶ
- **ambient と directional の分離**: 加重平均距離 `avgD = totalDistance / totalLight` で `asymmetry = dirLen / (totalLight * avgD)` を 0..1 で計算。**単一光源 → asymmetry ≈ 1**（片側だけ照らされる Lambertian）、**四方光源 → asymmetry ≈ 0**（全周一様の ambient）。`ambient = brightness * (1-asymmetry) * 0.85`、`directional = brightness * asymmetry`、`intensity = ambient + directional * max(0, n·L)`
- **球面サンプリング**: 敵を `stepPx` 刻みの矩形格子で塗る（通常敵 1.5px、ボス 3.0px）。各点に法線 `n = (sx/r, sy/r, sqrt(r² - sx² - sy²)/r)` を与え、光源ベクトルは画面平面 + `lz = 0.5` の 3D ベクトルに正規化。`lz` を浮かせるとハイライトが球面の内側に乗り、輪郭ではなく中心寄りが最も明るくなる
- **完全暗闇は描かない**: `totalLight <= 0` なら敵描画スキップ。ステージ 1 で「最初の斬撃を引いて初めて敵が見える」探索体験を成立させる土台

## ジャム制約

- 期間内に作ったものだけが対象。前年作品 (`pankona/egj2025`) からのコピーは禁止。`Makefile` / wasm 周りの定型部分は **参考にして書き直す** 方針
- 1 週間（〜2026-06-28 前後）の 1 人開発。残り時間が短いので、まず最小プロト → 手触り → ステージ拡張の順を崩さない
- 動詞「切る／囲んで光を奪い返す」がテーマ DISCONNECT を体験させる軸。「ただ避けるアクション」に薄まらない調整を優先する
