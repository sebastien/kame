class Kame < Formula
  desc "Build tool for projects described with Kame"
  homepage "https://github.com/sebastien/kame"
  version "0.4.0"

  on_macos do
    on_arm do
      url "https://github.com/sebastien/kame/releases/download/v0.4.0/kame-darwin-arm64"
      sha256 "c90b8d329530f49069e36d4cc78e333b6f47109fbbbaffb50672cba8b59389cd"
    end
    on_intel do
      url "https://github.com/sebastien/kame/releases/download/v0.4.0/kame-darwin-x86_64"
      sha256 "b1da5efa39d15753c2c6197584aaaf1c76e1a1b8bdb7227032d5399d6165b6ef"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/sebastien/kame/releases/download/v0.4.0/kame-linux-arm64"
      sha256 "b58991cfdb92d2a55a174cb288093d2085b21356a2ad5bdd48d5c37e062bedae"
    end
    on_intel do
      url "https://github.com/sebastien/kame/releases/download/v0.4.0/kame-linux-x86_64"
      sha256 "249be2c260b5a2b72dbf5db0096bd4e370d0af213f7c74f0bc0a6dc7da8f7e2b"
    end
  end

  def install
    bin.install Dir["kame-*"].first => "kame"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kame --version")
  end
end
