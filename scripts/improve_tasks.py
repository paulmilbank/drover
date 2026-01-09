#!/usr/bin/env python3
"""
Task Improvement Script for Drover

This script analyzes existing tasks and improves their descriptions
to be more specific and actionable for Claude execution.
"""

import sqlite3
import subprocess
import sys
import re
from pathlib import Path


def get_tasks_to_improve(db_path: str, status_filter: list = None) -> list:
    """Get tasks that need improvement from the database."""
    if status_filter is None:
        status_filter = ['ready', 'in_progress']

    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()

    placeholders = ','.join('?' * len(status_filter))
    cursor.execute(f"""
        SELECT id, title, description, epic_id, status
        FROM tasks
        WHERE status IN ({placeholders})
        ORDER BY created_at ASC
    """, status_filter)

    tasks = [dict(row) for row in cursor.fetchall()]
    conn.close()
    return tasks


def detect_project_type(project_path: str) -> str:
    """Detect the project type based on directory structure."""
    path = Path(project_path)

    # Check for various project indicators
    if (path / "go.mod").exists():
        return "Go"
    if (path / "package.json").exists():
        return "Node.js/JavaScript"
    if (path / "Cargo.toml").exists():
        return "Rust"
    if (path / "pyproject.toml").exists() or (path / "requirements.txt").exists():
        return "Python"
    if (path / "pom.xml").exists():
        return "Java/Maven"
    if (path / "build.gradle").exists():
        return "Java/Gradle"

    # Check for common frameworks
    if (path / "packages").exists():
        if (path / "packages" / "components").exists():
            return "Component library (monorepo)"
    if (path / "src" / "components").exists():
        return "Frontend component project"

    return "general software project"


def analyze_task_with_claude(title: str, description: str, project_path: str) -> str:
    """Use Claude to analyze and improve a task description."""
    project_type = detect_project_type(project_path)

    prompt = f"""I need you to improve this task description to make it more specific and actionable for a developer.

Current task:
Title: {title}
Description: {description}

Project context: This is a {project_type} at {project_path}

Please provide an improved description that:
1. Specifies exact files/packages to modify (use appropriate paths for this project type)
2. Includes specific action (create/update/fix/test/refactor)
3. Adds technical details (function names, feature flags, file paths, API endpoints, etc.)
4. Defines acceptance criteria (how to verify it works)
5. Is 2-4 sentences long

Respond ONLY with the improved description, nothing else."""

    try:
        result = subprocess.run(
            ['claude', '-p', prompt, '--dangerously-skip-permissions'],
            capture_output=True,
            text=True,
            timeout=30
        )

        if result.returncode == 0:
            improved = result.stdout.strip()
            # Remove Claude's conversational filler
            if improved.startswith("Here's") or improved.startswith("The improved"):
                lines = improved.split('\n')
                # Skip first line if it's conversational
                if len(lines) > 1:
                    improved = '\n'.join(lines[1:])
            return improved
        return description
    except Exception as e:
        print(f"  ⚠️  Claude error: {e}", file=sys.stderr)
        return description


def heuristic_improve(title: str, description: str) -> str:
    """Fallback heuristic-based task improvement."""
    improvements = []

    # Extract component name from title
    component_match = re.search(r'(\w+)\s+(component|variant|theme)', title, re.IGNORECASE)
    if component_match:
        component = component_match.group(1)
        improvements.append(f"- Target: {component} component")

    # Detect action from title
    action_map = {
        'add': 'Create new',
        'create': 'Create',
        'fix': 'Fix',
        'implement': 'Implement',
        'update': 'Update',
        'test': 'Test',
        'refactor': 'Refactor',
    }
    for keyword, action in action_map.items():
        if keyword in title.lower() or keyword in description.lower():
            improvements.append(f"- Action: {action}")
            break

    # Add file path suggestion
    improvements.append("- Files: packages/components/src/[component]/")

    # Add acceptance criteria
    improvements.append("- Acceptance: Verify by [running tests/building/checking output]")

    if improvements:
        return f"{description}\n\nSpecifics:\n" + "\n".join(improvements)

    return description


def improve_task(db_path: str, task_id: str, new_description: str) -> bool:
    """Update a task with an improved description."""
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()
    try:
        cursor.execute(
            "UPDATE tasks SET description = ? WHERE id = ?",
            (new_description, task_id)
        )
        conn.commit()
        return True
    except Exception as e:
        print(f"  ❌ Failed to update: {e}", file=sys.stderr)
        return False
    finally:
        conn.close()


def main():
    if len(sys.argv) > 1:
        db_path = sys.argv[1]
    else:
        db_path = ".drover/drover.db"

    if not Path(db_path).exists():
        print(f"❌ Database not found: {db_path}", file=sys.stderr)
        sys.exit(1)

    project_path = Path.cwd()

    print("🔧 Drover Task Improvement Script")
    print("=" * 60)
    print(f"Database: {db_path}")
    print(f"Project: {project_path}")
    print()

    # Get tasks to improve
    tasks = get_tasks_to_improve(db_path, status_filter=['ready', 'in_progress'])
    print(f"Found {len(tasks)} tasks to improve")
    print()

    if not tasks:
        print("✅ No tasks need improvement!")
        return

    # Check if Claude is available
    try:
        subprocess.run(['claude', '--version'], capture_output=True, check=True)
        use_claude = True
        print("🤖 Using Claude for task improvement")
    except:
        use_claude = False
        print("⚠️  Claude not found, using heuristic improvement")

    print()
    print("-" * 60)

    improved_count = 0
    skipped_count = 0

    for i, task in enumerate(tasks, 1):
        task_id = task['id']
        title = task['title']
        old_desc = task['description']

        print(f"\n[{i}/{len(tasks)}] {title}")
        print(f"  Old: {old_desc[:80]}..." if len(old_desc) > 80 else f"  Old: {old_desc}")

        # Skip if description is already good enough
        if len(old_desc) > 100 and 'packages/' in old_desc:
            print("  ✅ Already detailed, skipping")
            skipped_count += 1
            continue

        # Improve the description
        if use_claude:
            new_desc = analyze_task_with_claude(title, old_desc, str(project_path))
        else:
            new_desc = heuristic_improve(title, old_desc)

        # Check if actually improved
        if len(new_desc) <= len(old_desc) + 20:
            print("  ⚠️  No significant improvement, skipping")
            skipped_count += 1
            continue

        # Update the task
        if improve_task(db_path, task_id, new_desc):
            print(f"  ✅ Improved")
            print(f"  New: {new_desc[:80]}..." if len(new_desc) > 80 else f"  New: {new_desc}")
            improved_count += 1
        else:
            print(f"  ❌ Failed to update")

    print()
    print("=" * 60)
    print(f"✅ Improved {improved_count} tasks")
    print(f"⏭️  Skipped {skipped_count} tasks")
    print()
    print("Next: Run 'drover run' to execute the improved tasks!")


if __name__ == "__main__":
    main()
