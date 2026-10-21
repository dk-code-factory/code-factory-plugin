"""Local specification tooling, shared by the compatible script entry points.

Modules keep schema, snapshots, rendering, impact and verification separate.
Cross-module calls resolve the public facades at call time so existing import
APIs and patch points keep their behavior without eager import cycles.
"""
