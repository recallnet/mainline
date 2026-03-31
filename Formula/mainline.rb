class Mainline < Formula
  desc "Local-first protected-branch coordinator for Git worktrees"
  homepage "https://github.com/recallnet/mainline"
  head "https://github.com/recallnet/mainline.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-o", bin/"mainline", "./cmd/mainline"
    system "go", "build", "-trimpath", "-o", bin/"mq", "./cmd/mq"
    system "go", "build", "-trimpath", "-o", bin/"mainlined", "./cmd/mainlined"
  end

  test do
    assert_match "Usage:", shell_output("#{bin}/mainline --help")
    assert_match "Usage:", shell_output("#{bin}/mq --help")
    assert_match "background worker loop", shell_output("#{bin}/mainlined --help")
  end
end
