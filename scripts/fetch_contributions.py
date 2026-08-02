#!/usr/bin/env python3
"""
Fetch GitHub contribution data from public HTML endpoint.
No token required. Parses the contribution calendar.
Writes data/contributions.json with daily counts and stats.
"""

import requests
import json
from bs4 import BeautifulSoup
from datetime import datetime, timedelta

def fetch_contributions(username):
    """Fetch contribution calendar from GitHub public profile."""
    
    url = f"https://github.com/users/{username}/contributions"
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
    }
    
    print(f"Fetching contributions for @{username}...")
    response = requests.get(url, headers=headers, timeout=10)
    response.raise_for_status()
    
    soup = BeautifulSoup(response.text, "html.parser")

    # Find all contribution data cells.
    # Current GitHub markup uses <td class="ContributionCalendar-day"
    # with data-date / data-level. Older markup used <rect> inside <g>.
    contributions = {}

    cells = soup.find_all(attrs={"data-date": True})
    if not cells:
        for cell in soup.find_all("g"):
            cells.extend(cell.find_all("rect"))

    for cell in cells:
        date_str = cell.get("data-date")
        level = cell.get("data-level", "0")

        if date_str:
            try:
                contributions[date_str] = int(level)
            except (ValueError, TypeError):
                pass

    if not contributions:
        print("Warning: Could not parse contributions. Check username or try again.")
        return {}, {
            "total_contributions": 0,
            "current_streak": 0,
            "longest_streak": 0,
            "last_updated": datetime.now().isoformat(),
        }    
    # Calculate stats
    total_contributions = sum(1 for level in contributions.values() if level > 0)
    
    # Current streak
    today = datetime.now().date()
    current_streak = 0
    for i in range(365):
        check_date = (today - timedelta(days=i)).strftime("%Y-%m-%d")
        if check_date in contributions and contributions[check_date] > 0:
            current_streak += 1
        else:
            break
    
    # Longest streak (simplified)
    streak = 0
    longest_streak = 0
    for i in range(365, -1, -1):
        check_date = (today - timedelta(days=i)).strftime("%Y-%m-%d")
        if check_date in contributions and contributions[check_date] > 0:
            streak += 1
            longest_streak = max(longest_streak, streak)
        else:
            streak = 0
    
    stats = {
        "total_contributions": total_contributions,
        "current_streak": current_streak,
        "longest_streak": longest_streak,
        "last_updated": datetime.now().isoformat(),
    }
    
    return contributions, stats

def main():
    username = "ovalles2019"
    
    contributions, stats = fetch_contributions(username)
    
    # Prepare output directory
    import os
    os.makedirs("data", exist_ok=True)
    
    output = {
        "username": username,
        "contributions": contributions,
        "stats": stats,
    }
    
    with open("data/contributions.json", "w") as f:
        json.dump(output, f, indent=2)
    
    print(f"Saved contributions data to data/contributions.json")
    print(f"Total contributions in last year: {stats['total_contributions']}")
    print(f"Current streak: {stats['current_streak']} days")
    print(f"Longest streak: {stats['longest_streak']} days")

if __name__ == "__main__":
    main()
