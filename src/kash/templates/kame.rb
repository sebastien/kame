class Kame < Formula
  desc "Build tool for projects described with Kame"
  homepage "https://github.com/sebastien/kame"
  version "@{version}"

  on_macos do
    on_arm do
      url "@{base}/kame-darwin-arm64"
      sha256 "@{darwin_arm64}"
    end
    on_intel do
      url "@{base}/kame-darwin-x86_64"
      sha256 "@{darwin_x86_64}"
    end
  end

  on_linux do
    on_arm do
      url "@{base}/kame-linux-arm64"
      sha256 "@{linux_arm64}"
    end
    on_intel do
      url "@{base}/kame-linux-x86_64"
      sha256 "@{linux_x86_64}"
    end
  end

  def install
    bin.install Dir["kame-*"].first => "kame"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kame --version")
  end
end
