import React from 'react';

const ClockIcon: React.FC<{size?: number}> = ({size = 18}) => (
    <svg
        width={size}
        height={size}
        viewBox='0 0 24 24'
        fill='none'
        xmlns='http://www.w3.org/2000/svg'
        aria-hidden='true'
        focusable='false'
    >
        <circle cx='12' cy='12' r='9' stroke='currentColor' strokeWidth='1.8'/>
        <path d='M12 7v5l3 2' stroke='currentColor' strokeWidth='1.8' strokeLinecap='round' strokeLinejoin='round'/>
    </svg>
);

export default ClockIcon;
