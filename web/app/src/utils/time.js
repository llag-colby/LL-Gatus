/**
 * Generates a human-readable relative time string (e.g., "2 hours ago")
 * @param {string|Date} timestamp - The timestamp to convert
 * @returns {string} Relative time string
 */
export const generatePrettyTimeAgo = (timestamp, nowMs) => {
  const reference = nowMs != null ? nowMs : new Date().getTime();
  let differenceInMs = reference - new Date(timestamp).getTime();
  if (differenceInMs < 500) {
    return "now";
  }
  if (differenceInMs > 3 * 86400000) { // If it was more than 3 days ago, we'll display the number of days ago
    let days = (differenceInMs / 86400000).toFixed(0);
    return days + " day" + (days !== "1" ? "s" : "") + " ago";
  }
  if (differenceInMs > 3600000) { // If it was more than 1h ago, display the number of hours ago
    let hours = (differenceInMs / 3600000).toFixed(0);
    return hours + " hour" + (hours !== "1" ? "s" : "") + " ago";
  }
  if (differenceInMs > 60000) {
    let minutes = (differenceInMs / 60000).toFixed(0);
    return minutes + " minute" + (minutes !== "1" ? "s" : "") + " ago";
  }
  let seconds = (differenceInMs / 1000).toFixed(0);
  return seconds + " second" + (seconds !== "1" ? "s" : "") + " ago";
}

/**
 * Generates a pretty time difference string between two timestamps
 * @param {string|Date} start - Start timestamp
 * @param {string|Date} end - End timestamp
 * @returns {string} Time difference string
 */
export const generatePrettyTimeDifference = (start, end) => {
  const ms = new Date(start) - new Date(end)
  const seconds = Math.floor(ms / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)

  if (hours > 0) {
    const remainingMinutes = minutes % 60
    const hoursText = hours + (hours === 1 ? ' hour' : ' hours')
    if (remainingMinutes > 0) {
      return hoursText + ' ' + remainingMinutes + (remainingMinutes === 1 ? ' minute' : ' minutes')
    }
    return hoursText
  } else if (minutes > 0) {
    const remainingSeconds = seconds % 60
    const minutesText = minutes + (minutes === 1 ? ' minute' : ' minutes')
    if (remainingSeconds > 0) {
      return minutesText + ' ' + remainingSeconds + (remainingSeconds === 1 ? ' second' : ' seconds')
    }
    return minutesText
  } else {
    return seconds + (seconds === 1 ? ' second' : ' seconds')
  }
}

// All timestamps are displayed in US Central Time, 12-hour clock.
export const DISPLAY_TIMEZONE = 'America/Chicago'

/**
 * Formats a timestamp in Central Time, 12-hour format
 * (e.g., "07/08/2026, 7:43:08 PM CDT")
 * @param {string|Date} timestamp - The timestamp to format
 * @returns {string} Formatted timestamp
 */
export const prettifyTimestamp = (timestamp) => {
  const formatted = new Intl.DateTimeFormat('en-US', {
    timeZone: DISPLAY_TIMEZONE,
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: 'numeric', minute: '2-digit', second: '2-digit',
    hour12: true,
  }).format(new Date(timestamp));
  return `${formatted} CST`;
}