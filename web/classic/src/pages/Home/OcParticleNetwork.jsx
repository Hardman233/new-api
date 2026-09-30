/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect, useRef } from 'react';

const LINE_DISTANCE = 170;
const POINTER_DISTANCE = 220;

function getParticleCount(width, height) {
  const areaCount = Math.floor((width * height) / 13000);
  if (width < 640) {
    return Math.min(54, Math.max(34, areaCount));
  }
  return Math.min(126, Math.max(74, areaCount));
}

function createParticles(width, height) {
  const count = getParticleCount(width, height);
  return Array.from({ length: count }, () => {
    const speed = 0.09 + Math.random() * 0.18;
    const angle = Math.random() * Math.PI * 2;
    return {
      x: Math.random() * width,
      y: Math.random() * height,
      vx: Math.cos(angle) * speed,
      vy: Math.sin(angle) * speed,
      radius: 1.2 + Math.random() * 2.2,
    };
  });
}

function networkColor(alpha) {
  const isDark = document.documentElement.classList.contains('dark');
  if (isDark) {
    return `rgba(132, 170, 255, ${alpha})`;
  }
  return `rgba(37, 99, 235, ${alpha})`;
}

function pointColor(alpha) {
  const isDark = document.documentElement.classList.contains('dark');
  if (isDark) {
    return `rgba(96, 165, 250, ${alpha})`;
  }
  return `rgba(37, 99, 235, ${alpha})`;
}

export default function OcParticleNetwork() {
  const canvasRef = useRef(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const context = canvas?.getContext('2d');
    if (!canvas || !context) return undefined;

    let width = 0;
    let height = 0;
    let animationFrameId = 0;
    let particles = [];
    const pointer = { x: 0, y: 0, active: false };
    const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');

    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      width = Math.max(1, rect.width);
      height = Math.max(1, rect.height);
      canvas.width = Math.floor(width * dpr);
      canvas.height = Math.floor(height * dpr);
      context.setTransform(dpr, 0, 0, dpr, 0, 0);
      particles = createParticles(width, height);
    };

    const updatePointer = (event) => {
      const rect = canvas.getBoundingClientRect();
      pointer.x = event.clientX - rect.left;
      pointer.y = event.clientY - rect.top;
      pointer.active =
        pointer.x >= 0 &&
        pointer.y >= 0 &&
        pointer.x <= rect.width &&
        pointer.y <= rect.height;
    };

    const clearPointer = () => {
      pointer.active = false;
    };

    const drawConnections = () => {
      for (let i = 0; i < particles.length; i += 1) {
        const first = particles[i];
        for (let j = i + 1; j < particles.length; j += 1) {
          const second = particles[j];
          const dx = first.x - second.x;
          const dy = first.y - second.y;
          const distance = Math.hypot(dx, dy);
          if (distance > LINE_DISTANCE) continue;

          const alpha = (1 - distance / LINE_DISTANCE) * 0.18;
          context.strokeStyle = networkColor(alpha);
          context.lineWidth = 1;
          context.beginPath();
          context.moveTo(first.x, first.y);
          context.lineTo(second.x, second.y);
          context.stroke();
        }
      }
    };

    const drawPointerConnections = () => {
      if (!pointer.active) return;

      for (const particle of particles) {
        const dx = pointer.x - particle.x;
        const dy = pointer.y - particle.y;
        const distance = Math.hypot(dx, dy);
        if (distance > POINTER_DISTANCE || distance === 0) continue;

        const strength = 1 - distance / POINTER_DISTANCE;
        particle.x += (dx / distance) * strength * 0.52;
        particle.y += (dy / distance) * strength * 0.52;

        context.strokeStyle = networkColor(strength * 0.3);
        context.lineWidth = 1;
        context.beginPath();
        context.moveTo(pointer.x, pointer.y);
        context.lineTo(particle.x, particle.y);
        context.stroke();
      }
    };

    const drawParticles = () => {
      for (const particle of particles) {
        if (!reduceMotion.matches) {
          particle.x += particle.vx;
          particle.y += particle.vy;

          if (particle.x < -10) particle.x = width + 10;
          if (particle.x > width + 10) particle.x = -10;
          if (particle.y < -10) particle.y = height + 10;
          if (particle.y > height + 10) particle.y = -10;
        }

        context.fillStyle = pointColor(0.78);
        context.beginPath();
        context.arc(particle.x, particle.y, particle.radius, 0, Math.PI * 2);
        context.fill();
      }
    };

    const render = () => {
      context.clearRect(0, 0, width, height);
      drawConnections();
      drawPointerConnections();
      drawParticles();

      if (!reduceMotion.matches) {
        animationFrameId = window.requestAnimationFrame(render);
      }
    };

    resize();
    render();

    window.addEventListener('resize', resize);
    window.addEventListener('pointermove', updatePointer, { passive: true });
    window.addEventListener('pointerleave', clearPointer);
    window.addEventListener('blur', clearPointer);

    return () => {
      window.cancelAnimationFrame(animationFrameId);
      window.removeEventListener('resize', resize);
      window.removeEventListener('pointermove', updatePointer);
      window.removeEventListener('pointerleave', clearPointer);
      window.removeEventListener('blur', clearPointer);
    };
  }, []);

  return (
    <canvas
      ref={canvasRef}
      aria-hidden='true'
      className='pointer-events-none absolute inset-0 z-0 h-full w-full opacity-80 dark:opacity-60'
    />
  );
}
