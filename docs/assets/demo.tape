# The README demo (docs/assets/demo.gif). To regenerate after a release:
#   scripts/demo-env.sh      # kind cluster + published charts, users, policy
#   make demo                # renders this tape in the VHS container
# Record against a fresh environment: the audit step lists every attempt.
Output docs/assets/demo.gif

Set Shell bash
Set FontSize 15
Set Width 1000
Set Height 440
Set Padding 16
Set Framerate 20
Set TypingSpeed 40ms
Set Theme "Catppuccin Mocha"
Env HOME "/home/dev"

# 1. A developer signs in. No kubeconfig, no cluster credential, two clusters.
Type "kb login"
Enter
Sleep 800ms
Type "http://localhost:8080"
Enter
Sleep 600ms
Type "dev@example.com"
Enter
Sleep 600ms
Type "dev-password-123"
Enter
Sleep 1.5s
Type "kb clusters list"
Enter
Sleep 1.5s
Type "kb clusters use staging"
Enter
Sleep 1.5s

# 2. kubectl as usual inside the developer's namespace, reads and writes.
Hide
Type "clear"
Enter
Show
Type "kb get pods -n team-a"
Enter
Sleep 2.5s
Type "kb scale deploy web --replicas=3 -n team-a"
Enter
Sleep 2s

# 3. Outside it, the policy refuses and says who can help.
Type "kb get secrets -n kube-system"
Enter
Sleep 3.5s

# 4. The admin's view: bindings the agent keeps in sync.
Hide
Type "export HOME=/home/admin && clear"
Enter
Show
Type "# the admin's view of the same minute:"
Enter
Sleep 600ms
Type "kb admin rbac status"
Enter
Sleep 2.5s

# 5. The same command the developer was refused, allowed by the admin's role.
Type "kb get secrets -n kube-system"
Enter
Sleep 2.5s
Type "kb admin audit --user dev@example.com"
Enter
Sleep 5s
