// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

import '../vscodeMock';
import * as assert from 'assert';
import { FleetAgentItem, AgentStatus } from '../../fleetTreeDataProvider';
import { isValidAgentName } from '../../extension';

suite('Fleet Extension Test Suite', () => {
  test('isValidAgentName accepts valid agent identifiers', () => {
    assert.strictEqual(isValidAgentName('agent-1'), true);
    assert.strictEqual(isValidAgentName('coding_agent'), true);
    assert.strictEqual(isValidAgentName('devbox'), true);
    assert.strictEqual(isValidAgentName('a1'), true);
    assert.strictEqual(isValidAgentName('Agent-Alpha'), true);
  });

  test('isValidAgentName rejects CLI flags and malicious inputs', () => {
    assert.strictEqual(isValidAgentName('--all'), false);
    assert.strictEqual(isValidAgentName('-f'), false);
    assert.strictEqual(isValidAgentName(''), false);
    assert.strictEqual(isValidAgentName('   '), false);
    assert.strictEqual(isValidAgentName('a'), false); // Too short (min 2 chars)
    assert.strictEqual(isValidAgentName('../traversal'), false);
    assert.strictEqual(isValidAgentName('name;rm -rf /'), false);
    assert.strictEqual(isValidAgentName('agent with spaces'), false);
    assert.strictEqual(isValidAgentName('agent$foo'), false);
  });
  test('FleetAgentItem formatting for running agent', () => {
    const agent: AgentStatus = {
      name: 'test-agent',
      role: 'coding-agent',
      image: 'fleet-native:latest',
      container_name: 'fleet-agent-test-agent',
      container_state: 'running',
      ssh_port: 22222,
      ide_connect: 'agent@localhost:22222'
    };

    const item = new FleetAgentItem(agent);
    assert.strictEqual(item.label, 'test-agent');
    assert.strictEqual(item.contextValue, 'agent-running');
    assert.strictEqual(item.description, 'coding-agent (running, port:22222)');
    assert.ok(item.command);
    assert.strictEqual(item.command.command, 'fleet.attach');
  });

  test('FleetAgentItem formatting for stopped agent', () => {
    const agent: AgentStatus = {
      name: 'stopped-agent',
      role: 'triage-agent',
      image: 'fleet-core:latest',
      container_name: 'fleet-agent-stopped-agent',
      container_state: 'stopped',
      ssh_port: 0,
      ide_connect: '-'
    };

    const item = new FleetAgentItem(agent);
    assert.strictEqual(item.label, 'stopped-agent');
    assert.strictEqual(item.contextValue, 'agent-stopped');
    assert.strictEqual(item.description, 'triage-agent (stopped)');
  });

  test('FleetAgentItem formatting for HOST agent', () => {
    const agent: AgentStatus = {
      name: 'host-agent',
      role: 'host-runner',
      image: 'no-container',
      container_name: '-',
      container_state: 'HOST',
      ssh_port: 0,
      ide_connect: '-'
    };

    const item = new FleetAgentItem(agent);
    assert.strictEqual(item.label, 'host-agent');
    assert.strictEqual(item.contextValue, 'agent-host');
    assert.strictEqual(item.description, 'host-runner (HOST)');
  });
});
