class Kame < Formula
  desc "Build tool for projects described with Kame"
  homepage "https://github.com/sebastien/kame"
  url "https://github.com/sebastien/kame/releases/download/v0.2.3/kame.com"
  version "0.2.3"
  sha256 "aedbbc1037e4ea77de3ec687699a5898ddfd5552844c6d2cfb64f392ae201de0"

  def install
    bin.install "kame.com" => "kame"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kame --version")
  end
end
