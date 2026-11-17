#!/usr/bin/env node

const fs = require('fs');
const path = require('path');
const os = require('os');
const { execSync } = require('child_process');

const ANSI = {
  reset: '\x1b[0m',
  bold: '\x1b[1m',
  cyan: '\x1b[36m',
  green: '\x1b[32m',
  yellow: '\x1b[33m',
  red: '\x1b[31m',
  magenta: '\x1b[35m',
};

function copyDirRecursive(src, dest) {
  if (!fs.existsSync(dest)) {
    fs.mkdirSync(dest, { recursive: true });
  }

  const entries = fs.readdirSync(src, { withFileTypes: true });
  for (const entry of entries) {
    const srcPath = path.join(src, entry.name);
    const destPath = path.join(dest, entry.name);

    if (entry.isDirectory()) {
      copyDirRecursive(srcPath, destPath);
    } else {
      fs.copyFileSync(srcPath, destPath);
    }
  }
}

function printBanner() {
  console.log(`\n${ANSI.cyan}${ANSI.bold}====================================================${ANSI.reset}`);
  console.log(`${ANSI.cyan}${ANSI.bold}      Crypto Trader & Macro News Skills Installer    ${ANSI.reset}`);
  console.log(`${ANSI.cyan}${ANSI.bold}====================================================${ANSI.reset}\n`);
}

function resolveSourceDirs() {
  const rootDir = path.resolve(__dirname, '..');
  const skillsDir = path.join(rootDir, 'skills');
  const agentsDir = path.join(rootDir, 'agents');

  if (!fs.existsSync(skillsDir)) {
    console.error(`${ANSI.red}Error: Cannot find source skills directory at ${skillsDir}${ANSI.reset}`);
    process.exit(1);
  }

  return { skillsDir, agentsDir };
}

function detectTargets(args) {
  const homeDir = os.homedir();
  const cwd = process.cwd();
  const targets = [];

  const hasFlag = (flag) => args.includes(flag);
  const targetIndex = args.indexOf('--target');
  if (targetIndex !== -1 && args[targetIndex + 1]) {
    const customPath = path.resolve(args[targetIndex + 1]);
    targets.push({
      name: 'Custom Target',
      skillsDir: path.join(customPath, 'skills'),
      agentsDir: path.join(customPath, 'agents'),
    });
    return targets;
  }

  const installAll = hasFlag('--all');
  const installAntigravity = hasFlag('--antigravity') || installAll;
  const installClaude = hasFlag('--claude') || installAll;
  const installCursor = hasFlag('--cursor') || installAll;
  const isGlobal = hasFlag('--global');
  const isLocal = hasFlag('--local');

  if (!installAntigravity && !installClaude && !installCursor && !isGlobal && !isLocal) {
    targets.push({
      name: 'Local Antigravity Workspace (.agents)',
      skillsDir: path.join(cwd, '.agents', 'skills'),
      agentsDir: path.join(cwd, '.agents', 'agents'),
    });

    const claudeGlobal = path.join(homeDir, '.claude');
    if (fs.existsSync(claudeGlobal)) {
      targets.push({
        name: 'Global Claude Code (~/.claude)',
        skillsDir: path.join(claudeGlobal, 'skills'),
        agentsDir: path.join(claudeGlobal, 'agents'),
      });
    }

    const geminiGlobal = path.join(homeDir, '.gemini', 'config');
    if (fs.existsSync(geminiGlobal)) {
      targets.push({
        name: 'Global Antigravity (~/.gemini/config)',
        skillsDir: path.join(geminiGlobal, 'skills'),
        agentsDir: path.join(geminiGlobal, 'agents'),
      });
    }
  } else {
    if (installAntigravity || isGlobal) {
      targets.push({
        name: 'Global Antigravity (~/.gemini/config)',
        skillsDir: path.join(homeDir, '.gemini', 'config', 'skills'),
        agentsDir: path.join(homeDir, '.gemini', 'config', 'agents'),
      });
    }
    if (installAntigravity || isLocal) {
      targets.push({
        name: 'Local Antigravity (.agents)',
        skillsDir: path.join(cwd, '.agents', 'skills'),
        agentsDir: path.join(cwd, '.agents', 'agents'),
      });
    }
    if (installClaude || isGlobal) {
      targets.push({
        name: 'Global Claude Code (~/.claude)',
        skillsDir: path.join(homeDir, '.claude', 'skills'),
        agentsDir: path.join(homeDir, '.claude', 'agents'),
      });
    }
    if (installClaude || isLocal) {
      targets.push({
        name: 'Local Claude Code (.claude)',
        skillsDir: path.join(cwd, '.claude', 'skills'),
        agentsDir: path.join(cwd, '.claude', 'agents'),
      });
    }
    if (installCursor) {
      targets.push({
        name: 'Local Cursor (.cursor)',
        skillsDir: path.join(cwd, '.cursor', 'skills'),
        agentsDir: path.join(cwd, '.cursor', 'rules'),
      });
    }
  }

  return targets;
}

function checkGoEnvironment() {
  try {
    const version = execSync('go version', { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] });
    console.log(`${ANSI.green}✓ Go runtime detected:${ANSI.reset} ${version.trim()}`);
  } catch (e) {
    console.log(`${ANSI.yellow}! Note: 'go' was not detected in PATH. Make sure Go is installed to run the backend engines.${ANSI.reset}`);
  }
}

function install() {
  printBanner();
  checkGoEnvironment();

  const { skillsDir, agentsDir } = resolveSourceDirs();
  const args = process.argv.slice(2);
  const targets = detectTargets(args);

  const skillsList = fs.readdirSync(skillsDir).filter((f) => fs.statSync(path.join(skillsDir, f)).isDirectory());
  const agentsList = fs.existsSync(agentsDir)
    ? fs.readdirSync(agentsDir).filter((f) => f.endsWith('.md'))
    : [];

  console.log(`\n${ANSI.bold}Available Skills (${skillsList.length}):${ANSI.reset}`);
  for (const s of skillsList) {
    console.log(`  • ${ANSI.cyan}${s}${ANSI.reset}`);
  }

  if (agentsList.length > 0) {
    console.log(`\n${ANSI.bold}Available Subagents (${agentsList.length}):${ANSI.reset}`);
    for (const a of agentsList) {
      console.log(`  • ${ANSI.magenta}${a.replace('.md', '')}${ANSI.reset}`);
    }
  }

  console.log(`\n${ANSI.bold}Installing to ${targets.length} target environment(s)...${ANSI.reset}\n`);

  for (const target of targets) {
    console.log(`${ANSI.bold}Installing into:${ANSI.reset} ${ANSI.yellow}${target.name}${ANSI.reset}`);

    for (const skill of skillsList) {
      const srcSkill = path.join(skillsDir, skill);
      const destSkill = path.join(target.skillsDir, skill);
      copyDirRecursive(srcSkill, destSkill);
      console.log(`  ${ANSI.green}✓${ANSI.reset} Installed skill: ${skill} -> ${destSkill}`);
    }

    if (fs.existsSync(agentsDir)) {
      if (!fs.existsSync(target.agentsDir)) {
        fs.mkdirSync(target.agentsDir, { recursive: true });
      }
      for (const agentFile of agentsList) {
        const srcAgent = path.join(agentsDir, agentFile);
        const destAgent = path.join(target.agentsDir, agentFile);
        fs.copyFileSync(srcAgent, destAgent);
        console.log(`  ${ANSI.green}✓${ANSI.reset} Installed agent: ${agentFile} -> ${destAgent}`);
      }
    }
    console.log('');
  }

  console.log(`${ANSI.green}${ANSI.bold}Installation successfully completed!${ANSI.reset}\n`);
  console.log(`${ANSI.bold}Available Slash Commands in your Agent:${ANSI.reset}`);
  console.log(`  ${ANSI.cyan}/news-analysis${ANSI.reset} : Analyzes today's economic news releases (older news strictly excluded)`);
  console.log(`  ${ANSI.cyan}/news-weekly${ANSI.reset}   : Upcoming news for the week sorted by importance (High > Med > Low)`);
  console.log(`  ${ANSI.cyan}/news-monthly${ANSI.reset}  : Full month's macro mapping and danger date schedule\n`);
}

install();
