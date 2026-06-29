# タイトル決定の経緯と語彙体系

> **ステータス**: **`LIGHT MANDALA` で確定** (2026-06-29)。実装済み。
> 旧コードネーム `Rift` から差し替え、状態遷移文言も合わせて新語彙 (`LIT.` / `UNLIT.` / `ALL LIT.` / `LIT!`) に統一した。

## 確定した世界モデル

> 悪いものが世に染み出している。プレイヤーは光の線で**曼荼羅 (= 方陣)** を描き、囲い込んで封じる。封じきれなければ陣が破れる。悪いもの同士も同じ儀礼を逆向きに学んで、こちらを囲い返してくる。十の場所を巡り、最後の根を封じれば終わる。

「光で多角形を描いて閉じた領域を一気に光化する」という中心ギミックを、儀礼的な比喩としては **曼荼羅 (mandala)** が最も忠実に表す。

## なぜ `LIGHT MANDALA` か

タイトル選定は 4 つの軸を順に重ねて検討して着地した:

1. **単語数**: 1 単語 (`RIFT` / `SEAL`) はタイトル感が弱く、3 単語 (`SEAL THE DARK` / `HOLD THE LINE`) は長い。**2 単語が座りが最良**
2. **儀礼譚軸 (`LAST WARD` 候補)**: 「最後の結界」=「最後の儀礼継承者」という物語の輪郭を 2 単語で語る案。座りは良いが、`ward` の専門性 (病棟ともとれる) とトーンの重さがやや弱み
3. **DISCONNECT (テーマ) 軸**: 「線を引く = 断絶を作る」をタイトルに乗せる案 (`PART THE DARK` / `SEVER LINE` / `CLEAVE LIGHT`)。テーマ忠実だがアクション寄りに振れる
4. **曼荼羅軸**: ゲームのコア動作 (光線で閉じた幾何図形を描く) と完全に一致する儀礼語彙。**コード内に既に `stage-clear-mandala` という命名が走っている** ので、語彙の散らかりがない

採用案 `LIGHT MANDALA` の決め手:

- **2 単語タイトルの座り**: 形容詞 + 名詞、`LAST WARD` と同じ強い型
- **「光で曼荼羅を描く」 = プレイ動作の説明そのもの**: 題名がそのままメカニクスの宣言になる
- **コード語彙との合流**: `stage-clear-mandala` のコミット (`0770eb9`) が既にある。コード・演出・題名が一語で揃う
- **DISCONNECT 軸とも親和**: 曼荼羅は「世界の見取り図」 = 描くことで内と外を区切る儀礼。境界線を引いて断絶を作るゲーム動作と意味的に一致する
- **儀礼譚としての重さ**: サンスクリット由来で、神聖・幾何学的・儀礼的の三要素が一語に乗る
- **国際性**: `mandala` は英語圏でも借用語として通じる。日本人開発者が選ぶことに違和感がない

## 実装した語彙マッピング

| 用途 | 旧 | 新 | 場所 |
|---|---|---|---|
| ウィンドウタイトル | `Rift` | `Light Mandala` | `main.go` `ebiten.SetWindowTitle` |
| タイトル画面大見出し | `RIFT` | `LIGHT MANDALA` | `main.go` StateTitle 描画 |
| ステージクリア | `CLEAR` | `LIT.` | `main.go` StateCleared 描画 |
| 次ステージ表記 | `Next: Stage N` | `Next: N / 10` | `main.go` StateCleared 描画 |
| タイムアップ | `TIME UP` | `UNLIT.` | `main.go` StateGameOver 描画 |
| 全クリア | `ALL CLEAR` | `ALL LIT.` | `main.go` StateAllCleared 描画 |
| 中盤封印フラッシュ | `SEALED!` | `LIT!` | `main.go` `sealedFlashFrames` 描画 |
| HTML タイトル | `Rift` | `Light Mandala` | `web/game.html`, `web/index.html` |
| ローダー見出し | `RIFT` | `LIGHT MANDALA` | `web/game.html` `#loader-title` |
| README ヘッダ | `# Rift` | `# Light Mandala` | `README.md` |
| CLAUDE.md タイトル記述 | コードネーム `Rift` | タイトル `Light Mandala` (ビルド成果物名も `light-mandala` に統一) | `CLAUDE.md` |

### `LIT.` / `UNLIT.` / `LIT!` の対比

ステージクリア・タイムアップ・中盤フラッシュを **「曼荼羅が光で満ちたか / 満ちなかったか」の対比** で揃えた:

- 中盤フラッシュ `LIT!` (感嘆符): 「一つの領域に光が満ちた」イベント通知
- ステージクリア `LIT.` (句点): 「このステージの曼荼羅が完成した」状態宣言
- 全クリア `ALL LIT.`: 「十枚全ての曼荼羅が完成した」最終状態
- タイムアップ `UNLIT.`: 「曼荼羅が完成しなかった」失敗状態 — `LIT.` の鏡像

すべて短く、`faceLarge` の中央描画に綺麗に収まる。`SEALED!` の打点を `LIT!` で踏襲しつつ、語彙が曼荼羅軸に統一される。

### ローダー見出しの調整

`RIFT` (4 文字) → `LIGHT MANDALA` (13 文字) で大きく伸びるため、`web/game.html` の `#loader-title` を調整:

- `font-size: 44px` → `32px`
- `letter-spacing: 0.35em` → `0.22em`
- `white-space: nowrap` を追加 (折り返し防止)

これでモバイル幅 (320px) にも 1 行で収まる見込み。実機目視で気になれば font-size をさらに 28px まで下げる余地あり。

## 検討して却下した候補 (記録)

### 1 単語タイトル

- `RIFT` (初稿、現コードネーム): 「裂け目」だが意味が薄く、ゲーム動作を伝えない
- `SEAL`: 動詞列 `DRAG. SLASH. ENCLOSE. SEAL.` の延長すぎて、タイトル感が弱い
- `MANDALA` (単独): 1 単語の打点はあるが、ロゴ的に物足りない。`LIGHT MANDALA` の方が「光で描かれた」明示性で勝る

### 2-3 単語、儀礼譚軸

- `LAST WARD` (有力対抗): 「最後の結界」。物語の輪郭が綺麗だが、`ward` の二次意味 (病棟) と「最後の」の重さがミスマッチ
- `TENTH WARD` / `TEN WARDS`: 10 ステージ構造を題名化。具体的だが地名 (NYC) と被る
- `LAST SIGIL`: `sigil` の読みにくさが弱み
- `LIGHT WARD`: `Light` が動詞/形容詞の両義で曖昧

### 指示形 / 動詞 + 目的語

- `SEAL THE DARK`: 直接的だが、`Bumi` の `SEAL THE GAP` 等の先例多数
- `HOLD THE LINE`: 軍事英語の慣用句寄りで、サバイバル/TD トーンに寄る
- `CLOSE THE CIRCLE`: メカニクス直結だが `circle` は `mandala` より神秘性が薄い
- `PART THE DARK`: DISCONNECT 軸に最も忠実。曼荼羅軸採用で見送り

### DISCONNECT 軸 (線で断絶を作る)

- `SEVER LINE`: 最短 disconnect 表現。アクション寄りに振れすぎ
- `CLEAVE LIGHT`: `cleave` の二重意味 (裂く / 貼り付く) は面白いが、日本語話者に伝わりにくい
- `SUNDER LIGHT`: 古英語、読みにくい
- `RIFT WARD`: 現コードネーム `Rift` を残す折衷案。曼荼羅軸の方が強い

### 文学的余韻

- `AFTER LIGHT` / `AFTERLIGHT`: 残光モチーフ。儀礼感が薄い
- `NIGHT SEAL`: 同名作品多数、検索性 NG
- `INK AND SEAL`: `AND` が散漫
- `AFTER WARD`: pun タイトル。ジャムの一発ネタには良いが軽い

## 残り課題 / 将来の検討

- **ローダー見出しの目視確認**: モバイル (320px 幅) で `LIGHT MANDALA` が綺麗に収まっているか実機で確認。気になれば `font-size: 28px` まで下げる、または 2 行レイアウト (`LIGHT` / `MANDALA`) に切り替える
- **`LIT.` / `UNLIT.` の英語的自然さ**: `UNLIT.` は単語としては成立するが、ゲーム状態として読みやすいかは要プレイテスト。代案: `DIMMED.` / `BROKEN.` / `DARK.`
- **タイトル画面副題**: 現状なし。儀礼譚としての一言副題 (`Hold the dark.` / `Trace the light.` 等) を足すか検討
- **ステージクリア演出 `stage-clear-mandala`**: `LIT.` 文字とアニメ演出のタイミング合わせは別途。現状の `postClearCooldown` でカバーされている前提

## 影響範囲 (実装済み)

- `main.go`: タイトル / 状態遷移 / 中盤フラッシュの 7 箇所 + コメント 3 箇所
- `web/game.html`: HTML title, loader title, loader CSS の 3 箇所
- `web/index.html`: HTML title 1 箇所
- `README.md`: ヘッダ 1 箇所
- `CLAUDE.md`: コードネーム記述 + タイトル例の 2 箇所
- `docs/story-background-design.md`: 本ファイルの全面書き換え

ゲームロジック・チューニング定数には一切手を入れていない。
