# godefs

Goのソースコードをディレクトリ単位で再帰的に走査し、パッケージ名、ファイル名、関数・メソッドのシグネチャ、型定義、定数・変数宣言、ドキュメントコメントを標準出力に表示するCLIです。関数・メソッド宣言の本体と、対応する形式の変数初期化式に含まれる無名関数の本体を省いて、コードの構造を確認できます。

標準ライブラリの `go/parser` でソースコードを構文解析します。解析対象のコードをビルド・実行せず、ファイルも変更しません。外部モジュールへの依存はありません。

## 前提条件

- Go 1.27.3以上（`go.mod` の指定）
- TaskまたはMake（それぞれの補助コマンドを使う場合のみ）

## 使い方

このリポジトリのルートで実行します。

```bash
# カレントディレクトリを解析
go run .

# 指定したディレクトリを解析
go run . /path/to/project

# ヘルプとバージョンを表示
go run . -h
go run . -v

# 結果をファイルに保存（コメントのANSIエスケープシーケンスも含む）
go run . /path/to/project > definitions.txt
```

ビルドして使う場合は次のとおりです。

```bash
go build -o bin/godefs .
./bin/godefs /path/to/project
```

```text
godefs [options] [directory]
```

- 引数を省略すると `.` を解析します。相対パス・絶対パスを指定できます。
- オプションを除く引数が1つの場合、そのディレクトリを解析します。2つ以上の場合は、使い方を標準エラー出力に表示し、終了コード `2` で終了します。解析は行いません。
- `-v` はバージョンを標準出力に表示して終了します。通常の `go run . -v` や `go build` では `dev` です。解析やディレクトリ引数の個数確認は行いません。
- `-h` または `--help` はヘルプを標準エラー出力に表示して終了します。
- オプションはディレクトリの前に指定してください。最初の非オプション引数以降は、オプションとして解析しません。
- `-` で始まるディレクトリ名を指定する場合は、`godefs -- ./-example` のように `--` でオプションの解析を終了できます。

## 出力例

解析対象の `example.go` が次の内容の場合を示します。

```go
// Package example は挨拶を扱います。
package example

// Greeting は既定の挨拶です。
const Greeting = "こんにちは"

// DefaultName は既定の利用者名です。
var DefaultName = "ゲスト"

// User は利用者を表します。
type User struct {
	Name string
}

// Hello は挨拶を返します。
func (u User) Hello() string {
	return Greeting + u.Name
}
```

このファイルがあるディレクトリを指定すると、次のように表示されます。

```text
// Package example は挨拶を扱います。
package example (.)
  example.go
    // Greeting は既定の挨拶です。
    const Greeting = "こんにちは"

    // DefaultName は既定の利用者名です。
    var DefaultName = "ゲスト"

    // User は利用者を表します。
    type User struct {
    	Name string
    }

    // Hello は挨拶を返します。
    func (u User) Hello() string

```

上の出力例では、表示用のANSIエスケープシーケンスを省略しています。

`package example (.)` の括弧内は解析ルートからの相対ディレクトリです。サブディレクトリでは `internal/example` などと表示されます。

## 解析仕様

| 項目 | 動作 |
| --- | --- |
| 対象ファイル | 各ディレクトリ直下の `.go` ファイル。`_test.go` は除外 |
| 再帰走査 | サブディレクトリも解析。ルート配下の `.git`、`vendor`、`node_modules` は除外 |
| シンボリックリンク | ディレクトリのリンク先へは再帰しない。`.go` ファイルのリンク先は読み込む場合がある |
| パッケージ | ディレクトリごとに `package` 名で分類。複数あれば名前順で表示 |
| ファイル・宣言の順序 | ファイルは名前順、ファイル内の宣言はソースの記載順 |
| 関数・メソッド | 公開・非公開を問わず表示。レシーバー、引数、戻り値などを残し、関数本体を省略 |
| 型・定数・変数 | `type`・`const`・`var` の宣言を表示。構造体のフィールド、定数の値、変数の初期化式も含む。ただし、対応する形式の無名関数の本体は省略 |
| インポート | 宣言自体は表示しない。ただし宣言に付いたドキュメントコメントは表示される |
| コメント | パッケージ・宣言のドキュメントコメントを表示。型・定数・変数宣言内のコメントも整形時に含まれる場合がある |
| コメントの色 | 表示するコメントにANSIの淡色指定（`\x1b[2m`）とリセット（`\x1b[0m`）を付加 |
| 整形 | 宣言を `go/format` で整形し、字下げして表示 |

### 変数初期化式の無名関数

`var` 宣言では、初期化式の無名関数を引数・戻り値の型だけに置き換えます。直接代入する無名関数に加え、関数呼び出しの呼び出し先・引数、括弧式、複合リテラルの要素、キーと値の組の値を再帰的に処理します。

たとえば、次の宣言を解析すると、いずれも `return` を含む本体が省略されます。

```go
// Handler は挨拶を返します。
var Handler = func(name string) string { return "こんにちは" + name }

var Message = (func() string { return "こんにちは" })()
var Handlers = map[string]func() string{
	"hello": func() string { return "こんにちは" },
}
```

出力は次のとおりです（パッケージ・ファイル名、字下げ、ANSIエスケープシーケンスは省略）。

```text
// Handler は挨拶を返します。
var Handler = func(name string) string

var Message = (func() string)()

var Handlers = map[string]func() string{
	"hello": func() string,
}
```

これは構造を確認するための表示です。無名関数の本体を省いた出力は、そのままコンパイルできるGoコードとは限りません。

### 制限と注意点

- ビルドタグ、OS・アーキテクチャ別のファイル名、生成コードの除外には対応していません。条件に合うすべての `.go` ファイルを読みます。
- Goモジュールの境界では走査を止めません。`go.mod` がないディレクトリや、配下の別モジュールも解析します。
- 型検査や依存関係の解決は行いません。構文解析に成功すれば、型エラーがあるコードも表示できます。
- 変数の初期化式は、対応する無名関数の本体以外は省略しません。添字式・スライス式・型アサーション・セレクター式・単項式・二項式などの内部や、キーと値の組のキーは、本体省略のために再帰処理しません。たとえば `var Selected = []func() string{func() string { return "こんにちは" }}[0]` では無名関数の本体が残ります。
- コメントの淡色表示は常に有効です。ファイルへのリダイレクトやパイプでもANSIエスケープシーケンスが含まれます。色を無効にするオプションはありません。
- 定数の値、変数の初期化式、非公開の宣言も含まれます。結果を共有するときは内容を確認してください。

### エラーと終了状態

存在しないパス、ディレクトリ以外の指定、走査・読み取りの失敗、Goの構文エラーでは、標準エラー出力に理由を表示して終了コード `1` で終了します。処理途中で失敗した場合、それ以前の結果が標準出力に残ることがあります。

ディレクトリ引数が2つ以上の場合や、不明なオプション・不正なオプション値を指定した場合は終了コード `2` です。`-h`・`--help` と `-v` は正常終了（終了コード `0`）します。`go run` 経由では、プログラムの終了コード `2` も `go run` 自体の終了コードは `1` となり、標準エラー出力に `exit status 2` が追加されます。

宣言の整形に失敗した場合は、その位置に `<format error: ...>` を表示して処理を続けます。また、標準出力への書き込みエラーは終了状態に反映されません。

## 開発用コマンド

```bash
go test ./...
go vet ./...
```

現在、テストファイルはありません。上記はパッケージのビルドと静的チェックに利用できます。

MakefileとTaskfileには以下の補助コマンドがあります。

| 目的 | Make | Task |
| --- | --- | --- |
| ビルド | `make build`（実行環境向け） | `task build`（`build-mac` と `build-linux` を実行） |
| macOS向けビルド | — | `task build-mac`（macOS上で実行） |
| Linux向けビルド | `make build-linux` | `task build-linux` |
| Windows向けビルド | `make build-windows` | `task build-windows` |
| テスト | `make test` | `task test` |
| 脆弱性チェック | `make vuln` | `task vuln` |
| フォーマット | `make fmt` | `task fmt` |
| リント | `make lint` | `task lint` |

ビルド補助コマンドは先に `govulncheck` を実行し、未導入なら自動インストールします。リントも `golangci-lint` が未導入なら自動インストールします。これらの取得や脆弱性情報の照会にはネットワーク接続が必要です。

Makefile・Taskfileのビルドでは、`git describe --tags --abbrev=0` で取得したタグをバージョンに埋め込みます。タグを取得できない場合は空文字になり、`-v` は空行を出力します。

Linux・Windows向けはそれぞれAMD64とARM64を生成します。Linux向けは `CGO_ENABLED=0` でビルドします。

主な生成先は `make build` が `bin/godefs`、`task build-mac` が `bin/godefs-darwin`、Linux向けが `bin/godefs-linux-{amd64,arm64}`、Windows向けが `bin/godefs-windows-{amd64,arm64}.exe` です。

**Taskfileの注意点:** `task build-mac` は `GOOS`・`GOARCH` を指定せず、実行環境の設定でビルドして `-darwin` という名前を付けます。macOS以外で実行してもmacOS向けにはなりません。また、`task run` と `task install` は `bin/godefs` を参照しますが、依存先の `task build` はその名前のファイルを生成しません。現状は上記の `go build -o bin/godefs .` でビルドして直接実行してください。
